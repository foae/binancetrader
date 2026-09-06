package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	binance "github.com/adshao/go-binance/v2"
	"github.com/foae/binancetrader/storage"
	"github.com/shopspring/decimal"
)

// EventType distinguishes the source of a main loop trigger.
type EventType int

const (
	EventTick  EventType = iota // Regular 1-minute ticker
	EventAsync                  // External async event (websocket, webhook, etc.)
)

// TriggerEvent is sent into the main loop to trigger a tick.
type TriggerEvent struct {
	Type    EventType
	Source  string // Human-readable origin, e.g. "websocket", "webhook"
	Payload any    // Opaque data for the handler (unused for now)
}

// exchangeClient defines the exchange operations the service depends on.
type exchangeClient interface {
	Ping(ctx context.Context) error
	TickerPrice(ctx context.Context, symbol string) (string, error)
	CreateOrder(ctx context.Context, symbol string, side binance.SideType, orderType binance.OrderType, configure func(*binance.CreateOrderService)) (*binance.CreateOrderResponse, error)
	GetOrder(ctx context.Context, symbol string, orderID int64) (*binance.Order, error)
	CancelOrder(ctx context.Context, symbol string, orderID int64) (*binance.CancelOrderResponse, error)
	ListOpenOrders(ctx context.Context, symbol string) ([]*binance.Order, error)
	Spot() *binance.Client
}

// storageClient defines the storage operations the service depends on.
type storageClient interface {
	Close() error
	Set(ctx context.Context, table, id string, value any) error
	Get(ctx context.Context, table, id string, dest any) error
	Delete(ctx context.Context, table, id string) error
	List(ctx context.Context, table string, factory func() any) ([]any, error)
}

// Config holds service configuration.
type Config struct {
	Pairs           []PairConfig
	DryRun          bool
	StateID         string          // Account-key/environment fingerprint; never a credential.
	BuyOffset       decimal.Decimal // How far below market to place buy (e.g. 0.001 = 0.1%)
	BuyQuantityUSDT decimal.Decimal // USDT amount per buy order
	TakeProfit      decimal.Decimal // Sell target above entry (e.g. 0.01 = 1%)
	OrderExpiry     time.Duration   // Cancel open orders older than this
}

// Service is the single orchestrator that runs the main trading loop
// across all configured pairs.
type Service struct {
	exchange      exchangeClient
	db            storageClient
	cfg           Config
	ctx           context.Context
	triggerCh     chan TriggerEvent
	log           *slog.Logger
	symbolFilters map[string]*SymbolFilters
}

// New creates and starts the service. The main loop runs in a background
// goroutine and stops when ctx is cancelled.
func New(ctx context.Context, exchange exchangeClient, db storageClient, cfg Config) (*Service, error) {
	if len(cfg.Pairs) == 0 {
		return nil, fmt.Errorf("at least one pair must be configured")
	}

	svc := &Service{
		exchange:      exchange,
		db:            db,
		cfg:           cfg,
		ctx:           ctx,
		triggerCh:     make(chan TriggerEvent, 16),
		log:           slog.With("component", "service"),
		symbolFilters: make(map[string]*SymbolFilters),
	}
	if !cfg.DryRun {
		if err := svc.bindState(); err != nil {
			return nil, err
		}
	}

	go svc.run()

	return svc, nil
}

// bindState rejects legacy ledgers and accidental account/environment reuse.
// A single process must own each account and database; this is not a lock.
func (s *Service) bindState() error {
	if s.cfg.StateID == "" {
		return fmt.Errorf("state identity is required")
	}
	type stateIdentity struct {
		Schema int    `json:"schema"`
		ID     string `json:"id"`
	}
	var state stateIdentity
	err := s.db.Get(s.ctx, "metadata", "identity", &state)
	if err == nil {
		if state.Schema != 1 || state.ID != s.cfg.StateID {
			return fmt.Errorf("database schema/account/environment mismatch; reconcile before reuse")
		}
		return nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	for _, table := range []string{tableOrders, tablePositions, tableIntents} {
		rows, err := s.db.List(s.ctx, table, func() any { return &map[string]any{} })
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return fmt.Errorf("unversioned trading state; archive and reconcile against Binance before migration")
		}
	}
	return s.db.Set(s.ctx, "metadata", "identity", stateIdentity{Schema: 1, ID: s.cfg.StateID})
}

// Close releases service resources.
func (s *Service) Close() error {
	return nil
}

// Inject sends an async trigger event into the main loop.
// Non-blocking: drops the event if the channel buffer is full.
func (s *Service) Inject(evt TriggerEvent) {
	select {
	case s.triggerCh <- evt:
	default:
		s.log.Warn("Trigger channel full, dropping event", "source", evt.Source)
	}
}

// run is the main loop goroutine. It fires on a 1-minute ticker or on
// async events injected via triggerCh. Async events reset the ticker
// (debounce) so the next scheduled tick is always 1 minute from the
// last processed event.
func (s *Service) run() {
	l := s.log.With("loop", "main")

	if s.cfg.DryRun {
		l.Info("Running in DRY RUN mode")
	}

	l.Info("Main loop started", "pairs", s.cfg.Pairs)

	// Fire immediately on startup.
	s.tick(l)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			l.Info("Main loop stopped")
			return

		case evt := <-s.triggerCh:
			// Async event: process and reset ticker (debounce).
			ticker.Reset(1 * time.Minute)
			l.Info("Async trigger received", "source", evt.Source, "type", evt.Type)
			s.tick(l)

		case <-ticker.C:
			s.tick(l)
		}
	}
}

// tick runs one iteration of the main loop across all configured pairs.
func (s *Service) tick(l *slog.Logger) {
	l.Debug("Tick started")

	for _, pair := range s.cfg.Pairs {
		s.processPair(l, pair)
	}

	l.Debug("Tick completed")
}

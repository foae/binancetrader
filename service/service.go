package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
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
}

// storageClient defines the storage operations the service depends on.
type storageClient interface {
	Close() error
}

// Config holds service configuration.
type Config struct {
	Pairs  []string // Binance symbols: "BTCUSDC", "ETHUSDC"
	DryRun bool
}

// Service is the single orchestrator that runs the main trading loop
// across all configured pairs.
type Service struct {
	exchange  exchangeClient
	db        storageClient
	cfg       Config
	ctx       context.Context
	triggerCh chan TriggerEvent
	log       *slog.Logger
}

// New creates and starts the service. The main loop runs in a background
// goroutine and stops when ctx is cancelled.
func New(ctx context.Context, exchange exchangeClient, db storageClient, cfg Config) (*Service, error) {
	if len(cfg.Pairs) == 0 {
		return nil, fmt.Errorf("at least one pair must be configured")
	}

	svc := &Service{
		exchange:  exchange,
		db:        db,
		cfg:       cfg,
		ctx:       ctx,
		triggerCh: make(chan TriggerEvent, 16),
		log:       slog.With("component", "service"),
	}

	go svc.run()

	return svc, nil
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

// processPair handles a single pair during a tick.
func (s *Service) processPair(l *slog.Logger, pair string) {
	pl := l.With("pair", pair)

	// TODO: Implement trading strategy.
	// 1. Fetch current price via s.exchange.TickerPrice(ctx, pair)
	// 2. Evaluate indicators / signals
	// 3. Decide whether to enter / exit
	// 4. Place orders if criteria met

	pl.Debug("Processing pair")
}

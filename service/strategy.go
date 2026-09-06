package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	binance "github.com/adshao/go-binance/v2"
	"github.com/foae/binancetrader/storage"
	"github.com/shopspring/decimal"
)

const (
	tableOrders    = "orders"
	tablePositions = "positions"
	tableIntents   = "intents"
)

// An intent is durable BEFORE an exchange mutation. Uncertain submissions are
// recovered by client ID, never retried as a new order.
type orderIntent struct {
	ClientOrderID string          `json:"client_order_id"`
	Symbol        string          `json:"symbol"`
	Side          string          `json:"side"`
	Price         decimal.Decimal `json:"price"`
	Quantity      decimal.Decimal `json:"quantity"`
	CreatedAt     time.Time       `json:"created_at"`
}

func (s *Service) processPair(l *slog.Logger, pair PairConfig) {
	l = l.With("pair", pair.Symbol)
	if err := s.processPairOnce(l, pair); err != nil {
		l.Error("Trading paused for symbol", "error", err)
	}
}

func (s *Service) processPairOnce(l *slog.Logger, pair PairConfig) error {
	// Dry-run is a stateless intent preview, not an exchange fill simulator.
	if s.cfg.DryRun {
		filters, err := s.ensureFilters(l, pair.Symbol)
		if err != nil {
			return err
		}
		price, err := s.marketPrice(pair.Symbol)
		if err != nil {
			return err
		}
		return s.placeBuyOrder(l, pair, price, filters)
	}
	if err := s.recoverIntent(pair); err != nil {
		return err
	}
	orders, err := s.loadOrders(pair.Symbol)
	if err != nil {
		return err
	}
	for i := range orders {
		rec := &orders[i]
		active, err := activeStatus(rec.Status)
		if err != nil {
			return err
		}
		if !active {
			continue
		}
		order, err := s.exchange.GetOrder(s.ctx, pair.Symbol, rec.OrderID)
		if err != nil {
			return fmt.Errorf("query order %d: %w", rec.OrderID, err)
		}
		if err := s.updateOrder(rec, order); err != nil {
			return err
		}
		active, err = activeStatus(rec.Status)
		if err != nil {
			return err
		}
		if active && time.Since(rec.CreatedAt) >= s.cfg.OrderExpiry {
			// Read authoritative final state even when cancellation races a fill.
			_, cancelErr := s.exchange.CancelOrder(s.ctx, pair.Symbol, rec.OrderID)
			final, queryErr := s.exchange.GetOrder(s.ctx, pair.Symbol, rec.OrderID)
			if queryErr != nil {
				return fmt.Errorf("query cancellation outcome: %w", queryErr)
			}
			if err := s.updateOrder(rec, final); err != nil {
				return err
			}
			stillActive, err := activeStatus(rec.Status)
			if err != nil {
				return err
			}
			if stillActive {
				return fmt.Errorf("cancellation unresolved for %d (cancel error: %v)", rec.OrderID, cancelErr)
			}
		}
	}
	pos, active, err := derivePosition(pair.Symbol, orders)
	if err != nil {
		return err
	}
	// The position is only a cache; decisions use the replay result above.
	if pos.Quantity.IsPositive() {
		if err := s.db.Set(s.ctx, tablePositions, pair.Symbol, &pos); err != nil {
			return err
		}
	} else if err := s.db.Delete(s.ctx, tablePositions, pair.Symbol); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	open, err := s.exchange.ListOpenOrders(s.ctx, pair.Symbol)
	if err != nil {
		return err
	}
	known := make(map[int64]bool, len(orders))
	for _, o := range orders {
		known[o.OrderID] = true
	}
	for _, o := range open {
		if !known[o.OrderID] {
			return fmt.Errorf("unmanaged open order %d; reconcile manually", o.OrderID)
		}
	}
	// Never sell an active partial buy, nor replace an active partial sell.
	if active || len(open) > 0 {
		return nil
	}
	filters, err := s.ensureFilters(l, pair.Symbol)
	if err != nil {
		return err
	}
	if pos.Quantity.IsPositive() {
		return s.placeSellOrder(l, pair, &pos, filters)
	}
	price, err := s.marketPrice(pair.Symbol)
	if err != nil {
		return err
	}
	return s.placeBuyOrder(l, pair, price, filters)
}

func (s *Service) marketPrice(symbol string) (decimal.Decimal, error) {
	text, err := s.exchange.TickerPrice(s.ctx, symbol)
	if err != nil {
		return decimal.Zero, err
	}
	price, err := decimal.NewFromString(text)
	if err != nil || !price.IsPositive() {
		return decimal.Zero, fmt.Errorf("invalid market price")
	}
	return price, nil
}

func activeStatus(status string) (bool, error) {
	switch status {
	case "NEW", "PARTIALLY_FILLED", "PENDING_CANCEL":
		return true, nil
	case "FILLED", "CANCELED", "EXPIRED", "EXPIRED_IN_MATCH", "REJECTED":
		return false, nil
	default:
		return false, fmt.Errorf("unknown order status %q", status)
	}
}

func (s *Service) loadOrders(symbol string) ([]OrderRecord, error) {
	all, err := s.db.List(s.ctx, tableOrders, func() any { return &OrderRecord{} })
	if err != nil {
		return nil, err
	}
	orders := make([]OrderRecord, 0, len(all))
	seen := map[int64]bool{}
	for _, value := range all {
		rec, ok := value.(*OrderRecord)
		if !ok {
			return nil, fmt.Errorf("invalid order record type")
		}
		if rec.Symbol != symbol {
			continue
		}
		if seen[rec.OrderID] {
			return nil, fmt.Errorf("duplicate order identity %d", rec.OrderID)
		}
		seen[rec.OrderID] = true
		orders = append(orders, *rec)
	}
	return orders, nil
}

// Replay cumulative snapshots, never deltas. Orders cannot overlap under this
// strategy; average cost is reduced proportionally by each sell execution.
func derivePosition(symbol string, orders []OrderRecord) (Position, bool, error) {
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return orders[i].OrderID < orders[j].OrderID
		}
		return orders[i].CreatedAt.Before(orders[j].CreatedAt)
	})
	pos := Position{Symbol: symbol}
	cost := decimal.Zero
	active := false
	for _, o := range orders {
		open, err := activeStatus(o.Status)
		if err != nil {
			return pos, false, err
		}
		if o.OrderID <= 0 || o.CreatedAt.IsZero() || !o.Quantity.IsPositive() || !o.Price.IsPositive() || o.ExecutedQuantity.IsNegative() || o.ExecutedQuantity.GreaterThan(o.Quantity) || o.QuoteQuantity.IsNegative() {
			return pos, false, fmt.Errorf("invalid ledger order %d", o.OrderID)
		}
		if o.Side != "BUY" && o.Side != "SELL" {
			return pos, false, fmt.Errorf("invalid order side")
		}
		if o.ExecutedQuantity.IsPositive() && !o.QuoteQuantity.IsPositive() {
			return pos, false, fmt.Errorf("missing execution cost for order %d; legacy state requires reconciliation", o.OrderID)
		}
		if active {
			return pos, false, fmt.Errorf("overlapping order history requires manual reconciliation")
		}
		active = open
		if o.ExecutedQuantity.IsZero() {
			continue
		}
		if o.Side == "BUY" {
			if pos.Quantity.IsZero() {
				pos.CreatedAt = o.CreatedAt
				pos.BuyOrderID = o.OrderID
			}
			pos.Quantity = pos.Quantity.Add(o.ExecutedQuantity)
			cost = cost.Add(o.QuoteQuantity)
		} else {
			if o.ExecutedQuantity.GreaterThan(pos.Quantity) {
				return pos, false, fmt.Errorf("sell exceeds tracked inventory")
			}
			remaining := pos.Quantity.Sub(o.ExecutedQuantity)
			if remaining.IsZero() {
				cost = decimal.Zero
			} else {
				cost = cost.Mul(remaining).Div(pos.Quantity)
			}
			pos.Quantity = remaining
		}
		pos.UpdatedAt = o.UpdatedAt
	}
	if pos.Quantity.IsPositive() {
		pos.EntryPrice = cost.Div(pos.Quantity)
	}
	return pos, active, nil
}

func (s *Service) updateOrder(rec *OrderRecord, o *binance.Order) error {
	if o.Symbol != rec.Symbol || o.OrderID != rec.OrderID || string(o.Side) != rec.Side {
		return fmt.Errorf("exchange order identity mismatch")
	}
	if rec.ClientOrderID != "" && o.ClientOrderID != rec.ClientOrderID {
		return fmt.Errorf("exchange client order ID mismatch")
	}
	if _, err := activeStatus(string(o.Status)); err != nil {
		return err
	}
	executed, err := decimal.NewFromString(o.ExecutedQuantity)
	if err != nil {
		return fmt.Errorf("invalid executed quantity: %w", err)
	}
	quote, err := decimal.NewFromString(o.CummulativeQuoteQuantity)
	if err != nil {
		return fmt.Errorf("invalid cumulative quote quantity: %w", err)
	}
	qty, err := decimal.NewFromString(o.OrigQuantity)
	if err != nil || !qty.Equal(rec.Quantity) {
		return fmt.Errorf("exchange order quantity mismatch")
	}
	if executed.LessThan(rec.ExecutedQuantity) || executed.GreaterThan(rec.Quantity) || quote.LessThan(rec.QuoteQuantity) {
		return fmt.Errorf("inconsistent cumulative execution")
	}
	if executed.IsPositive() && !quote.IsPositive() {
		return fmt.Errorf("missing cumulative execution cost")
	}
	rec.ExecutedQuantity = executed
	rec.QuoteQuantity = quote
	rec.Status = string(o.Status)
	rec.UpdatedAt = time.Now()
	return s.saveOrder(rec)
}

func (s *Service) recoverIntent(pair PairConfig) error {
	var intent orderIntent
	err := s.db.Get(s.ctx, tableIntents, pair.Symbol, &intent)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if intent.Symbol != pair.Symbol || intent.ClientOrderID == "" || intent.CreatedAt.IsZero() {
		return fmt.Errorf("invalid pending intent")
	}
	order, err := s.exchange.Spot().NewGetOrderService().Symbol(pair.Symbol).OrigClientOrderID(intent.ClientOrderID).Do(s.ctx)
	if err != nil {
		return fmt.Errorf("unresolved submission %s; do not retry or clear without exchange reconciliation: %w", intent.ClientOrderID, err)
	}
	rec := OrderRecord{Symbol: pair.Symbol, OrderID: order.OrderID, ClientOrderID: intent.ClientOrderID, Side: intent.Side, Price: intent.Price, Quantity: intent.Quantity, CreatedAt: intent.CreatedAt}
	if err := s.updateOrder(&rec, order); err != nil {
		return err
	}
	return s.db.Delete(s.ctx, tableIntents, pair.Symbol)
}

func (s *Service) submitOrder(pair PairConfig, side binance.SideType, price, qty decimal.Decimal) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	intent := orderIntent{ClientOrderID: "bt-" + hex.EncodeToString(nonce[:]), Symbol: pair.Symbol, Side: string(side), Price: price, Quantity: qty, CreatedAt: time.Now()}
	if err := s.db.Set(s.ctx, tableIntents, pair.Symbol, &intent); err != nil {
		return err
	}
	_, err := s.exchange.CreateOrder(s.ctx, pair.Symbol, side, binance.OrderTypeLimit, func(order *binance.CreateOrderService) {
		order.TimeInForce(binance.TimeInForceTypeGTC).Price(price.String()).Quantity(qty.String()).NewClientOrderID(intent.ClientOrderID)
	})
	if err != nil {
		return fmt.Errorf("submission uncertain; durable intent retained: %w", err)
	}
	// Even immediate fills use the same recoverable snapshot path.
	if err := s.recoverIntent(pair); err != nil {
		return err
	}
	orders, err := s.loadOrders(pair.Symbol)
	if err != nil {
		return err
	}
	pos, _, err := derivePosition(pair.Symbol, orders)
	if err != nil {
		return err
	}
	if pos.Quantity.IsPositive() {
		return s.db.Set(s.ctx, tablePositions, pair.Symbol, &pos)
	}
	err = s.db.Delete(s.ctx, tablePositions, pair.Symbol)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	return err
}

func (s *Service) saveOrder(rec *OrderRecord) error {
	return s.db.Set(s.ctx, tableOrders, fmt.Sprintf("%s:%d", rec.Symbol, rec.OrderID), rec)
}

func (s *Service) placeBuyOrder(l *slog.Logger, pair PairConfig, market decimal.Decimal, filters *SymbolFilters) error {
	price := roundToTickSize(market.Mul(decimal.NewFromInt(1).Sub(s.cfg.BuyOffset)), filters.TickSize)
	if !price.IsPositive() {
		return fmt.Errorf("buy price must be positive")
	}
	qty := roundToStepSize(s.cfg.BuyQuantityUSDT.Div(price), filters.StepSize)
	// The budget is a ceiling, not permission to silently increase spending.
	if err := validateOrder(price, qty, filters); err != nil {
		return err
	}
	if s.cfg.DryRun {
		l.Info("[DRY RUN] Would place buy order", "price", price, "qty", qty)
		return nil
	}
	return s.submitOrder(pair, binance.SideTypeBuy, price, qty)
}

func (s *Service) placeSellOrder(l *slog.Logger, pair PairConfig, pos *Position, filters *SymbolFilters) error {
	price := roundToTickSize(pos.EntryPrice.Mul(decimal.NewFromInt(1).Add(s.cfg.TakeProfit)), filters.TickSize)
	qty := roundToStepSize(pos.Quantity, filters.StepSize)
	if err := validateOrder(price, qty, filters); err != nil {
		return err
	}
	if s.cfg.DryRun {
		l.Info("[DRY RUN] Would place sell order", "price", price, "qty", qty)
		return nil
	}
	return s.submitOrder(pair, binance.SideTypeSell, price, qty)
}

func validateOrder(price, qty decimal.Decimal, f *SymbolFilters) error {
	if !price.IsPositive() || !qty.IsPositive() || qty.LessThan(f.MinQty) || qty.GreaterThan(f.MaxQty) || price.LessThan(f.MinPrice) || price.GreaterThan(f.MaxPrice) || price.Mul(qty).LessThan(f.MinNotional) {
		return fmt.Errorf("order outside exchange filters; adjust budget or reconcile dust")
	}
	return nil
}

func (s *Service) ensureFilters(l *slog.Logger, symbol string) (*SymbolFilters, error) {
	if f, ok := s.symbolFilters[symbol]; ok {
		return f, nil
	}
	info, err := s.exchange.Spot().NewExchangeInfoService().Symbol(symbol).Do(s.ctx)
	if err != nil {
		return nil, err
	}
	if len(info.Symbols) != 1 || info.Symbols[0].Symbol != symbol {
		return nil, fmt.Errorf("unexpected symbol filters")
	}
	sym := info.Symbols[0]
	lot := sym.LotSizeFilter()
	price := sym.PriceFilter()
	if lot == nil || price == nil {
		return nil, fmt.Errorf("missing lot or price filter")
	}
	f := &SymbolFilters{}
	fields := []struct {
		dest *decimal.Decimal
		raw  string
	}{{&f.MinQty, lot.MinQuantity}, {&f.MaxQty, lot.MaxQuantity}, {&f.StepSize, lot.StepSize}, {&f.MinPrice, price.MinPrice}, {&f.MaxPrice, price.MaxPrice}, {&f.TickSize, price.TickSize}}
	for _, field := range fields {
		*field.dest, err = decimal.NewFromString(field.raw)
		if err != nil || !field.dest.IsPositive() {
			return nil, fmt.Errorf("invalid symbol filter")
		}
	}
	if n := sym.NotionalFilter(); n != nil {
		f.MinNotional, err = decimal.NewFromString(n.MinNotional)
	} else {
		var raw string
		for _, filter := range sym.Filters {
			if filter["filterType"] == "MIN_NOTIONAL" {
				raw, _ = filter["minNotional"].(string)
			}
		}
		f.MinNotional, err = decimal.NewFromString(raw)
	}
	if err != nil || f.MinNotional.IsNegative() {
		return nil, fmt.Errorf("invalid notional filter")
	}
	s.symbolFilters[symbol] = f
	return f, nil
}

func roundToTickSize(price, tick decimal.Decimal) decimal.Decimal {
	if tick.IsZero() {
		return price
	}
	return price.Div(tick).Floor().Mul(tick)
}

func roundToStepSize(qty, step decimal.Decimal) decimal.Decimal {
	if step.IsZero() {
		return qty
	}
	return qty.Div(step).Floor().Mul(step)
}

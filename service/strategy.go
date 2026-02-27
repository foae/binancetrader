package service

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	binance "github.com/adshao/go-binance/v2"
	"github.com/foae/binancetrader/storage"
	"github.com/shopspring/decimal"
)

const (
	tableOrders    = "orders"
	tablePositions = "positions"
)

// processPair handles a single pair during a tick. Decision flow:
//  1. Ensure symbol filters are cached
//  2. Fetch market price
//  3. Sync local order records against Binance API
//  4. Cancel expired GTC orders
//  5. Evaluate position state and act
func (s *Service) processPair(l *slog.Logger, pair PairConfig) {
	pl := l.With("pair", pair.Symbol)

	filters, err := s.ensureFilters(pl, pair.Symbol)
	if err != nil {
		pl.Error("Failed to load symbol filters", "error", err)
		return
	}

	priceStr, err := s.exchange.TickerPrice(s.ctx, pair.Symbol)
	if err != nil {
		pl.Error("Failed to fetch market price", "error", err)
		return
	}
	marketPrice, err := decimal.NewFromString(priceStr)
	if err != nil {
		pl.Error("Invalid market price", "price", priceStr, "error", err)
		return
	}

	pl.Info("Market price", "price", marketPrice)

	s.syncOrders(pl, pair)
	s.checkExpiredOrders(pl, pair)

	// Load position.
	var pos Position
	hasPosition := true
	if err := s.db.Get(s.ctx, tablePositions, pair.Symbol, &pos); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			hasPosition = false
		} else {
			pl.Error("Failed to load position", "error", err)
			return
		}
	}

	// Check for open orders in DB.
	hasOpenBuy, hasOpenSell := s.hasOpenOrders(pl, pair.Symbol)

	switch {
	case hasPosition && hasOpenSell:
		pl.Debug("Position open, sell order active — waiting")
	case hasPosition && !hasOpenSell:
		threshold := pos.EntryPrice.Mul(decimal.NewFromInt(1).Add(s.cfg.TakeProfit))
		if marketPrice.GreaterThanOrEqual(threshold) {
			pl.Info("Market crossed take-profit, selling",
				"market", marketPrice, "threshold", threshold, "entry", pos.EntryPrice,
			)
			s.placeSellOrder(pl, pair, &pos, marketPrice, filters)
		} else {
			pl.Debug("Holding position, below take-profit",
				"market", marketPrice, "threshold", threshold, "entry", pos.EntryPrice,
			)
		}
	case !hasPosition && hasOpenBuy:
		pl.Debug("No position, buy order active — waiting")
	case !hasPosition && !hasOpenBuy:
		pl.Info("No position, no buy order — placing limit buy")
		s.placeBuyOrder(pl, pair, marketPrice, filters)
	}
}

// ensureFilters fetches and caches symbol filters (lot size, price filter) on first call.
func (s *Service) ensureFilters(l *slog.Logger, symbol string) (*SymbolFilters, error) {
	if f, ok := s.symbolFilters[symbol]; ok {
		return f, nil
	}

	l.Info("Fetching exchange info for symbol filters")

	info, err := s.exchange.Spot().NewExchangeInfoService().Symbol(symbol).Do(s.ctx)
	if err != nil {
		return nil, fmt.Errorf("exchange info: %w", err)
	}
	if len(info.Symbols) == 0 {
		return nil, fmt.Errorf("no symbol info returned for %s", symbol)
	}

	sym := info.Symbols[0]
	f := &SymbolFilters{}

	if lot := sym.LotSizeFilter(); lot != nil {
		f.MinQty, _ = decimal.NewFromString(lot.MinQuantity)
		f.MaxQty, _ = decimal.NewFromString(lot.MaxQuantity)
		f.StepSize, _ = decimal.NewFromString(lot.StepSize)
	}
	if price := sym.PriceFilter(); price != nil {
		f.MinPrice, _ = decimal.NewFromString(price.MinPrice)
		f.MaxPrice, _ = decimal.NewFromString(price.MaxPrice)
		f.TickSize, _ = decimal.NewFromString(price.TickSize)
	}

	l.Info("Symbol filters cached",
		"min_qty", f.MinQty, "step_size", f.StepSize,
		"tick_size", f.TickSize,
	)

	s.symbolFilters[symbol] = f
	return f, nil
}

// syncOrders reconciles local DB order records against the Binance API.
// Orders that are OPEN in DB but missing from Binance are queried individually
// to determine their final status (filled, cancelled, etc.).
func (s *Service) syncOrders(l *slog.Logger, pair PairConfig) {
	dbOrders := s.loadOpenOrders(l, pair.Symbol)
	if len(dbOrders) == 0 {
		return
	}

	// Build set of Binance open order IDs.
	binanceOrders, err := s.exchange.ListOpenOrders(s.ctx, pair.Symbol)
	if err != nil {
		l.Error("Failed to list open orders from Binance", "error", err)
		return
	}
	openOnBinance := make(map[int64]struct{}, len(binanceOrders))
	for _, o := range binanceOrders {
		openOnBinance[o.OrderID] = struct{}{}
	}

	for _, rec := range dbOrders {
		if _, stillOpen := openOnBinance[rec.OrderID]; stillOpen {
			// Update executed quantity from Binance.
			for _, bo := range binanceOrders {
				if bo.OrderID == rec.OrderID {
					execQty, _ := decimal.NewFromString(bo.ExecutedQuantity)
					if !execQty.Equal(rec.ExecutedQuantity) {
						rec.ExecutedQuantity = execQty
						rec.UpdatedAt = time.Now()
						s.saveOrder(l, &rec)
					}
					break
				}
			}
			continue
		}

		// Order no longer open on Binance — query final status.
		order, err := s.exchange.GetOrder(s.ctx, pair.Symbol, rec.OrderID)
		if err != nil {
			l.Error("Failed to query order status", "order_id", rec.OrderID, "error", err)
			continue
		}

		execQty, _ := decimal.NewFromString(order.ExecutedQuantity)
		rec.ExecutedQuantity = execQty
		rec.Status = string(order.Status)
		rec.UpdatedAt = time.Now()
		s.saveOrder(l, &rec)

		l.Info("Order status updated",
			"order_id", rec.OrderID, "side", rec.Side,
			"status", rec.Status, "executed_qty", rec.ExecutedQuantity,
		)

		switch order.Status {
		case binance.OrderStatusTypeFilled:
			s.handleFill(l, pair, &rec)
		case binance.OrderStatusTypeCanceled, binance.OrderStatusTypeExpired, binance.OrderStatusTypeRejected:
			if rec.ExecutedQuantity.IsPositive() {
				// Partial fill before cancel — treat as fill for the executed qty.
				s.handleFill(l, pair, &rec)
			}
		}
	}
}

// handleFill processes a filled (or partially filled) order.
// Buy fill → create position. Sell fill → delete position.
func (s *Service) handleFill(l *slog.Logger, pair PairConfig, rec *OrderRecord) {
	switch rec.Side {
	case string(binance.SideTypeBuy):
		pos := &Position{
			Symbol:     pair.Symbol,
			Quantity:   rec.ExecutedQuantity,
			EntryPrice: rec.Price,
			BuyOrderID: rec.OrderID,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := s.db.Set(s.ctx, tablePositions, pair.Symbol, pos); err != nil {
			l.Error("Failed to save position after buy fill", "error", err)
			return
		}
		l.Info("Position created from buy fill",
			"qty", pos.Quantity, "entry_price", pos.EntryPrice,
		)

	case string(binance.SideTypeSell):
		if err := s.db.Delete(s.ctx, tablePositions, pair.Symbol); err != nil && !errors.Is(err, storage.ErrNotFound) {
			l.Error("Failed to delete position after sell fill", "error", err)
			return
		}
		l.Info("Position closed from sell fill",
			"qty", rec.ExecutedQuantity, "price", rec.Price,
		)
	}
}

// checkExpiredOrders cancels GTC orders older than ORDER_EXPIRY.
func (s *Service) checkExpiredOrders(l *slog.Logger, pair PairConfig) {
	dbOrders := s.loadOpenOrders(l, pair.Symbol)
	now := time.Now()

	for _, rec := range dbOrders {
		age := now.Sub(rec.CreatedAt)
		if age < s.cfg.OrderExpiry {
			continue
		}

		l.Info("Cancelling expired order",
			"order_id", rec.OrderID, "side", rec.Side, "age", age.Round(time.Second),
		)

		if s.cfg.DryRun {
			l.Info("[DRY RUN] Would cancel expired order", "order_id", rec.OrderID)
			continue
		}

		_, err := s.exchange.CancelOrder(s.ctx, pair.Symbol, rec.OrderID)
		if err != nil {
			l.Error("Failed to cancel expired order", "order_id", rec.OrderID, "error", err)
			continue
		}

		rec.Status = string(binance.OrderStatusTypeCanceled)
		rec.UpdatedAt = time.Now()
		s.saveOrder(l, &rec)
	}
}

// placeBuyOrder places a GTC limit buy below the market price.
func (s *Service) placeBuyOrder(l *slog.Logger, pair PairConfig, marketPrice decimal.Decimal, filters *SymbolFilters) {
	// Price = market * (1 - BUY_OFFSET)
	price := marketPrice.Mul(decimal.NewFromInt(1).Sub(s.cfg.BuyOffset))
	price = roundToTickSize(price, filters.TickSize)

	if price.LessThanOrEqual(decimal.Zero) {
		l.Warn("Computed buy price <= 0, skipping", "market", marketPrice, "offset", s.cfg.BuyOffset)
		return
	}

	// Qty = BUY_QUANTITY_USDC / price
	qty := s.cfg.BuyQuantityUSDC.Div(price)
	qty = roundToStepSize(qty, filters.StepSize)

	if qty.LessThan(filters.MinQty) {
		l.Warn("Computed buy quantity below minimum",
			"qty", qty, "min_qty", filters.MinQty, "price", price,
		)
		return
	}

	l.Info("Placing buy order",
		"price", price, "qty", qty, "market", marketPrice,
	)

	if s.cfg.DryRun {
		l.Info("[DRY RUN] Would place buy order", "price", price, "qty", qty)
		s.saveDryRunOrder(l, pair.Symbol, binance.SideTypeBuy, price, qty)
		return
	}

	resp, err := s.exchange.CreateOrder(s.ctx, pair.Symbol, binance.SideTypeBuy, binance.OrderTypeLimit,
		func(svc *binance.CreateOrderService) {
			svc.TimeInForce(binance.TimeInForceTypeGTC)
			svc.Price(price.String())
			svc.Quantity(qty.String())
		},
	)
	if err != nil {
		l.Error("Failed to place buy order", "error", err)
		return
	}

	rec := &OrderRecord{
		Symbol:           pair.Symbol,
		OrderID:          resp.OrderID,
		Side:             string(binance.SideTypeBuy),
		Price:            price,
		Quantity:         qty,
		ExecutedQuantity: decimal.Zero,
		Status:           string(resp.Status),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	s.saveOrder(l, rec)

	l.Info("Buy order placed", "order_id", resp.OrderID, "status", resp.Status)
}

// placeSellOrder places a market sell. Called only after confirming market
// price has crossed the take-profit threshold.
func (s *Service) placeSellOrder(l *slog.Logger, pair PairConfig, pos *Position, marketPrice decimal.Decimal, filters *SymbolFilters) {
	qty := roundToStepSize(pos.Quantity, filters.StepSize)

	if qty.LessThan(filters.MinQty) {
		l.Warn("Position quantity below minimum for sell",
			"qty", qty, "min_qty", filters.MinQty,
		)
		return
	}

	l.Info("Placing market sell",
		"qty", qty, "market", marketPrice, "entry_price", pos.EntryPrice,
	)

	if s.cfg.DryRun {
		l.Info("[DRY RUN] Would place market sell", "qty", qty, "market", marketPrice)
		rec := &OrderRecord{
			Symbol:           pair.Symbol,
			OrderID:          time.Now().UnixMilli(),
			Side:             string(binance.SideTypeSell),
			Price:            marketPrice,
			Quantity:         qty,
			ExecutedQuantity: qty,
			Status:           string(binance.OrderStatusTypeFilled),
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}
		s.saveOrder(l, rec)
		if err := s.db.Delete(s.ctx, tablePositions, pair.Symbol); err != nil && !errors.Is(err, storage.ErrNotFound) {
			l.Error("Failed to delete position after dry-run sell", "error", err)
		}
		return
	}

	resp, err := s.exchange.CreateOrder(s.ctx, pair.Symbol, binance.SideTypeSell, binance.OrderTypeMarket,
		func(svc *binance.CreateOrderService) {
			svc.Quantity(qty.String())
		},
	)
	if err != nil {
		l.Error("Failed to place sell order", "error", err)
		return
	}

	execQty, _ := decimal.NewFromString(resp.ExecutedQuantity)
	rec := &OrderRecord{
		Symbol:           pair.Symbol,
		OrderID:          resp.OrderID,
		Side:             string(binance.SideTypeSell),
		Price:            marketPrice,
		Quantity:         qty,
		ExecutedQuantity: execQty,
		Status:           string(resp.Status),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	s.saveOrder(l, rec)

	l.Info("Sell order placed", "order_id", resp.OrderID, "status", resp.Status, "executed_qty", execQty)

	// Market orders typically fill immediately. Clean up position now.
	if resp.Status == binance.OrderStatusTypeFilled {
		if err := s.db.Delete(s.ctx, tablePositions, pair.Symbol); err != nil && !errors.Is(err, storage.ErrNotFound) {
			l.Error("Failed to delete position after sell fill", "error", err)
		} else {
			l.Info("Position closed", "qty", execQty)
		}
	}
	// If not filled immediately (unlikely for market), syncOrders handles it next tick.
}

// saveDryRunOrder saves a simulated order record for dry-run mode tracking.
func (s *Service) saveDryRunOrder(l *slog.Logger, symbol string, side binance.SideType, price, qty decimal.Decimal) {
	rec := &OrderRecord{
		Symbol:           symbol,
		OrderID:          time.Now().UnixMilli(), // synthetic ID for dry-run
		Side:             string(side),
		Price:            price,
		Quantity:         qty,
		ExecutedQuantity: decimal.Zero,
		Status:           "NEW",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	s.saveOrder(l, rec)
}

// --- Helpers ---

// loadOpenOrders returns all order records from DB with status NEW or PARTIALLY_FILLED for a symbol.
func (s *Service) loadOpenOrders(l *slog.Logger, symbol string) []OrderRecord {
	all, err := s.db.List(s.ctx, tableOrders, func() any { return &OrderRecord{} })
	if err != nil {
		l.Error("Failed to list orders from DB", "error", err)
		return nil
	}

	var open []OrderRecord
	for _, v := range all {
		rec, ok := v.(*OrderRecord)
		if !ok || rec.Symbol != symbol {
			continue
		}
		if rec.Status == string(binance.OrderStatusTypeNew) || rec.Status == string(binance.OrderStatusTypePartiallyFilled) {
			open = append(open, *rec)
		}
	}
	return open
}

// hasOpenOrders checks whether there are open buy/sell orders for a symbol.
func (s *Service) hasOpenOrders(l *slog.Logger, symbol string) (hasBuy, hasSell bool) {
	orders := s.loadOpenOrders(l, symbol)
	for _, o := range orders {
		switch o.Side {
		case string(binance.SideTypeBuy):
			hasBuy = true
		case string(binance.SideTypeSell):
			hasSell = true
		}
	}
	return
}

// saveOrder persists an order record to the DB.
func (s *Service) saveOrder(l *slog.Logger, rec *OrderRecord) {
	id := fmt.Sprintf("%s:%d", rec.Symbol, rec.OrderID)
	if err := s.db.Set(s.ctx, tableOrders, id, rec); err != nil {
		l.Error("Failed to save order record", "order_id", rec.OrderID, "error", err)
	}
}

// roundToTickSize floors a price to the nearest tick size.
func roundToTickSize(price, tickSize decimal.Decimal) decimal.Decimal {
	if tickSize.IsZero() {
		return price
	}
	return price.Div(tickSize).Floor().Mul(tickSize)
}

// roundToStepSize floors a quantity to the nearest step size.
func roundToStepSize(qty, stepSize decimal.Decimal) decimal.Decimal {
	if stepSize.IsZero() {
		return qty
	}
	return qty.Div(stepSize).Floor().Mul(stepSize)
}


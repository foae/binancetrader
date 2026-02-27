package service

import (
	"time"

	"github.com/shopspring/decimal"
)

// PairConfig holds the parsed representation of a trading pair.
type PairConfig struct {
	Symbol string // Binance symbol: "BTCUSDC"
	Base   string // Base asset: "BTC"
	Quote  string // Quote asset: "USDC"
}

// OrderRecord is a local mirror of a Binance order, stored in Redis.
// Key scheme: orders:{symbol}:{orderID}
type OrderRecord struct {
	Symbol           string          `json:"symbol"`
	OrderID          int64           `json:"order_id"`
	Side             string          `json:"side"` // "BUY" or "SELL"
	Price            decimal.Decimal `json:"price"`
	Quantity         decimal.Decimal `json:"quantity"`
	ExecutedQuantity decimal.Decimal `json:"executed_quantity"`
	Status           string          `json:"status"` // "NEW", "PARTIALLY_FILLED", "FILLED", "CANCELED", etc.
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// Position represents inventory for a symbol. One position per symbol max.
// Key scheme: positions:{symbol}
type Position struct {
	Symbol     string          `json:"symbol"`
	Quantity   decimal.Decimal `json:"quantity"`
	EntryPrice decimal.Decimal `json:"entry_price"`
	BuyOrderID int64           `json:"buy_order_id"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// SymbolFilters holds cached exchange info for lot/price rounding.
type SymbolFilters struct {
	MinQty      decimal.Decimal
	MaxQty      decimal.Decimal
	StepSize    decimal.Decimal
	MinPrice    decimal.Decimal
	MaxPrice    decimal.Decimal
	TickSize    decimal.Decimal
	MinNotional decimal.Decimal
}

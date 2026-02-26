package exchange

import (
	"context"
	"fmt"
	"time"

	binance "github.com/adshao/go-binance/v2"
)

// Client wraps the go-binance SDK for Binance spot trading.
//
// NOTE: WebSocket support is intentionally excluded. The go-binance WS
// implementation has a confirmed data race in reconnection handling
// (ccxt/go-binance#800, opened 2026-01-24). We will implement our own
// WebSocket layer on top of gorilla/websocket when streaming is needed.
type Client struct {
	spot *binance.Client
}

// Option configures the client at creation time.
type Option func()

// WithTestnet enables the Binance testnet endpoints.
// Requires separate testnet API keys from https://testnet.binance.vision/
func WithTestnet() Option {
	return func() {
		binance.UseTestnet = true
	}
}

// NewClient creates a new Binance spot client.
func NewClient(apiKey, apiSecret string, opts ...Option) *Client {
	for _, opt := range opts {
		opt()
	}
	return &Client{
		spot: binance.NewClient(apiKey, apiSecret),
	}
}

// Spot returns the underlying go-binance client for direct access to the
// full API surface and builder pattern. Use this for operations not covered
// by the convenience methods below.
func (c *Client) Spot() *binance.Client {
	return c.spot
}

// --- Public endpoints ---

// Ping tests connectivity to the Binance API.
func (c *Client) Ping(ctx context.Context) error {
	return c.spot.NewPingService().Do(ctx)
}

// ServerTime returns the Binance server time.
func (c *Client) ServerTime(ctx context.Context) (time.Time, error) {
	ms, err := c.spot.NewServerTimeService().Do(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(ms), nil
}

// TickerPrice returns the latest price for a symbol (e.g., "BTCUSDC").
// Returns the price as a string to preserve decimal precision.
func (c *Client) TickerPrice(ctx context.Context, symbol string) (string, error) {
	prices, err := c.spot.NewListPricesService().Symbol(symbol).Do(ctx)
	if err != nil {
		return "", err
	}
	if len(prices) == 0 {
		return "", fmt.Errorf("no price returned for %s", symbol)
	}
	return prices[0].Price, nil
}

// Klines returns candlestick data for a symbol and interval.
// Interval examples: "1m", "5m", "15m", "1h", "1d".
func (c *Client) Klines(ctx context.Context, symbol, interval string, limit int) ([]*binance.Kline, error) {
	return c.spot.NewKlinesService().
		Symbol(symbol).
		Interval(interval).
		Limit(limit).
		Do(ctx)
}

// --- Authenticated endpoints ---

// Account returns account information including balances.
func (c *Client) Account(ctx context.Context) (*binance.Account, error) {
	return c.spot.NewGetAccountService().Do(ctx)
}

// CreateOrder places a new spot order. Use the configure callback to set
// quantity, price, time-in-force, etc. via the go-binance builder:
//
//	resp, err := client.CreateOrder(ctx, "BTCUSDC", binance.SideTypeBuy,
//	    binance.OrderTypeLimit, func(s *binance.CreateOrderService) {
//	        s.TimeInForce(binance.TimeInForceTypeGTC)
//	        s.Quantity("0.001")
//	        s.Price("50000.00")
//	    })
func (c *Client) CreateOrder(
	ctx context.Context,
	symbol string,
	side binance.SideType,
	orderType binance.OrderType,
	configure func(*binance.CreateOrderService),
) (*binance.CreateOrderResponse, error) {
	svc := c.spot.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(orderType)
	if configure != nil {
		configure(svc)
	}
	return svc.Do(ctx)
}

// GetOrder returns the status of an existing order.
func (c *Client) GetOrder(ctx context.Context, symbol string, orderID int64) (*binance.Order, error) {
	return c.spot.NewGetOrderService().
		Symbol(symbol).
		OrderID(orderID).
		Do(ctx)
}

// CancelOrder cancels an open order.
func (c *Client) CancelOrder(ctx context.Context, symbol string, orderID int64) (*binance.CancelOrderResponse, error) {
	return c.spot.NewCancelOrderService().
		Symbol(symbol).
		OrderID(orderID).
		Do(ctx)
}

// ListOpenOrders returns all open orders, optionally filtered by symbol.
// Pass an empty symbol to list across all symbols.
func (c *Client) ListOpenOrders(ctx context.Context, symbol string) ([]*binance.Order, error) {
	svc := c.spot.NewListOpenOrdersService()
	if symbol != "" {
		svc.Symbol(symbol)
	}
	return svc.Do(ctx)
}

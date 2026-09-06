package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	binance "github.com/adshao/go-binance/v2"
	"github.com/foae/binancetrader/exchange"
	"github.com/foae/binancetrader/storage"
	"github.com/shopspring/decimal"
)

type memoryStore struct {
	data              map[string][]byte
	failList, failSet bool
}

func (m *memoryStore) Close() error { return nil }
func (m *memoryStore) Set(_ context.Context, table, id string, v any) error {
	if m.failSet {
		return errors.New("storage unavailable")
	}
	b, e := json.Marshal(v)
	m.data[table+":"+id] = b
	return e
}

func (m *memoryStore) Get(_ context.Context, table, id string, v any) error {
	b, ok := m.data[table+":"+id]
	if !ok {
		return storage.ErrNotFound
	}
	return json.Unmarshal(b, v)
}

func (m *memoryStore) Delete(_ context.Context, table, id string) error {
	key := table + ":" + id
	if _, ok := m.data[key]; !ok {
		return storage.ErrNotFound
	}
	delete(m.data, key)
	return nil
}

func (m *memoryStore) List(_ context.Context, table string, factory func() any) ([]any, error) {
	if m.failList {
		return nil, errors.New("storage unavailable")
	}
	var out []any
	for k, b := range m.data {
		if len(k) > len(table) && k[:len(table)+1] == table+":" {
			v := factory()
			if err := json.Unmarshal(b, v); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	}
	return out, nil
}

type tradingFixture struct {
	svc                       *Service
	db                        *memoryStore
	orders                    map[int64]*binance.Order
	creates, queries, cancels int
	cancelFill                string
	rejectCreate              bool
	failAfterAccept           bool
}

func newTradingFixture(t *testing.T) *tradingFixture {
	t.Helper()
	f := &tradingFixture{db: &memoryStore{data: map[string][]byte{}}, orders: map[int64]*binance.Order{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/exchangeInfo":
			io.WriteString(w, `{"symbols":[{"symbol":"BTCUSDT","filters":[{"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"},{"filterType":"PRICE_FILTER","minPrice":"0.01","maxPrice":"1000000","tickSize":"0.01"},{"filterType":"NOTIONAL","minNotional":"1"}]}]}`)
		case "/api/v3/ticker/price":
			io.WriteString(w, `{"symbol":"BTCUSDT","price":"100"}`)
		case "/api/v3/openOrders":
			var orders []*binance.Order
			for _, o := range f.orders {
				if o.Status == binance.OrderStatusTypeNew || o.Status == binance.OrderStatusTypePartiallyFilled {
					orders = append(orders, o)
				}
			}
			if orders == nil {
				orders = []*binance.Order{}
			}
			json.NewEncoder(w).Encode(orders)
		case "/api/v3/order":
			r.ParseForm()
			switch r.Method {
			case http.MethodPost:
				f.creates++
				if f.rejectCreate {
					w.WriteHeader(400)
					io.WriteString(w, `{"code":-2010,"msg":"rejected"}`)
					return
				}
				id := int64(100 + f.creates)
				o := &binance.Order{Symbol: "BTCUSDT", OrderID: id, ClientOrderID: r.Form.Get("newClientOrderId"), Side: binance.SideType(r.Form.Get("side")), Price: r.Form.Get("price"), OrigQuantity: r.Form.Get("quantity"), ExecutedQuantity: "0", CummulativeQuoteQuantity: "0", Status: binance.OrderStatusTypeNew}
				f.orders[id] = o
				if f.failAfterAccept {
					o.Status = binance.OrderStatusTypeFilled
					o.ExecutedQuantity = o.OrigQuantity
					f.db.failSet = true
				}
				json.NewEncoder(w).Encode(o)
			case http.MethodGet, http.MethodDelete:
				f.queries++
				id, _ := strconv.ParseInt(r.Form.Get("orderId"), 10, 64)
				o := f.orders[id]
				if id == 0 {
					for _, candidate := range f.orders {
						if candidate.ClientOrderID == r.Form.Get("origClientOrderId") {
							o = candidate
							break
						}
					}
				}
				if o == nil {
					w.WriteHeader(400)
					io.WriteString(w, `{"code":-2013,"msg":"unknown order"}`)
					return
				}
				if r.Method == http.MethodDelete {
					f.cancels++
					o.Status = binance.OrderStatusTypeCanceled
					if f.cancelFill != "" {
						o.ExecutedQuantity = f.cancelFill
					}
				}
				o.CummulativeQuoteQuantity = decimal.RequireFromString(o.ExecutedQuantity).Mul(decimal.NewFromInt(100)).String()
				json.NewEncoder(w).Encode(o)
			}
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	client := exchange.NewClient("test-key", "test-secret")
	client.Spot().BaseURL = server.URL
	f.svc = &Service{ctx: context.Background(), exchange: client, db: f.db, cfg: Config{Pairs: []PairConfig{{Symbol: "BTCUSDT", Base: "BTC", Quote: "USDT"}}, BuyOffset: decimal.RequireFromString("0.001"), BuyQuantityUSDT: decimal.NewFromInt(5), TakeProfit: decimal.RequireFromString("0.01"), OrderExpiry: time.Hour}, symbolFilters: map[string]*SymbolFilters{}}
	return f
}

func (f *tradingFixture) tick() {
	f.svc.processPair(slog.New(slog.NewTextHandler(io.Discard, nil)), f.svc.cfg.Pairs[0])
}

func (f *tradingFixture) seed(t *testing.T, id int64, side, status, qty, executed string, age time.Duration) {
	t.Helper()
	rec := OrderRecord{Symbol: "BTCUSDT", OrderID: id, Side: side, Price: decimal.NewFromInt(100), Quantity: decimal.RequireFromString(qty), ExecutedQuantity: decimal.RequireFromString(executed), QuoteQuantity: decimal.RequireFromString(executed).Mul(decimal.NewFromInt(100)), Status: status, CreatedAt: time.Now().Add(-age)}
	if e := f.db.Set(context.Background(), tableOrders, "BTCUSDT:"+strconv.FormatInt(id, 10), rec); e != nil {
		t.Fatal(e)
	}
	f.orders[id] = &binance.Order{Symbol: "BTCUSDT", OrderID: id, Side: binance.SideType(side), Price: "100", OrigQuantity: qty, ExecutedQuantity: executed, CummulativeQuoteQuantity: rec.QuoteQuantity.String(), Status: binance.OrderStatusType(status)}
}

func (f *tradingFixture) position(t *testing.T) Position {
	t.Helper()
	var p Position
	if err := f.db.Get(context.Background(), tablePositions, "BTCUSDT", &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPartialSellCancellationPreservesInventory(t *testing.T) {
	f := newTradingFixture(t)
	f.seed(t, 1, "BUY", "FILLED", "10", "10", 3*time.Hour)
	f.seed(t, 2, "SELL", "NEW", "10", "0", 2*time.Hour)
	f.db.Set(context.Background(), tablePositions, "BTCUSDT", Position{Symbol: "BTCUSDT", Quantity: decimal.NewFromInt(10), EntryPrice: decimal.NewFromInt(100), BuyOrderID: 1})
	f.orders[2].Status = binance.OrderStatusTypeCanceled
	f.orders[2].ExecutedQuantity = "4"
	f.tick()
	p := f.position(t)
	if !p.Quantity.Equal(decimal.NewFromInt(6)) {
		t.Fatalf("remaining quantity = %s, want 6", p.Quantity)
	}
	f.tick()
	p = f.position(t)
	if !p.Quantity.Equal(decimal.NewFromInt(6)) {
		t.Fatalf("replay changed remaining quantity to %s", p.Quantity)
	}
	if f.creates != 1 {
		t.Fatalf("created %d orders, want one replacement sell", f.creates)
	}
}

func TestExpiryCapturesCancellationFill(t *testing.T) {
	f := newTradingFixture(t)
	f.seed(t, 1, "BUY", "NEW", "10", "0", 2*time.Hour)
	f.cancelFill = "4"
	f.tick()
	if f.cancels != 1 {
		t.Fatalf("cancel calls=%d", f.cancels)
	}
	p := f.position(t)
	if !p.Quantity.Equal(decimal.NewFromInt(4)) {
		t.Fatalf("position=%s, want canceled buy's 4 filled units", p.Quantity)
	}
}

func TestStorageFailureBlocksOrderCreation(t *testing.T) {
	for _, kind := range []string{"list", "write", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			f := newTradingFixture(t)
			if kind == "list" {
				f.db.failList = true
			}
			if kind == "write" {
				f.db.failSet = true
			}
			if kind == "corrupt" {
				f.db.data["orders:BTCUSDT:1"] = []byte("invalid")
			}
			f.tick()
			if f.creates != 0 {
				t.Fatalf("created %d orders during storage failure", f.creates)
			}
		})
	}
}

func TestDryRunDoesNotPersistOrReconcileSyntheticOrders(t *testing.T) {
	f := newTradingFixture(t)
	f.svc.cfg.DryRun = true
	f.tick()
	f.tick()
	if f.creates != 0 || f.queries != 0 || f.cancels != 0 {
		t.Fatalf("dry run touched authenticated order endpoints: creates=%d queries=%d cancels=%d", f.creates, f.queries, f.cancels)
	}
	if len(f.db.data) != 0 {
		t.Fatalf("dry run persisted %d records", len(f.db.data))
	}
}

func TestUnmanagedOpenOrderBlocksTrading(t *testing.T) {
	f := newTradingFixture(t)
	f.orders[42] = &binance.Order{Symbol: "BTCUSDT", OrderID: 42, Status: binance.OrderStatusTypeNew}
	f.tick()
	if f.creates != 0 {
		t.Fatal("created an order alongside an unmanaged exchange order")
	}
}

func TestAcceptedFillRecoveredAfterStorageFailure(t *testing.T) {
	f := newTradingFixture(t)
	f.failAfterAccept = true
	f.tick()
	if f.creates != 1 {
		t.Fatalf("creates=%d", f.creates)
	}
	f.db.failSet = false
	f.failAfterAccept = false
	// Reconstruct the service-owned caches to model a restart.
	f.svc.symbolFilters = map[string]*SymbolFilters{}
	f.tick()
	if f.creates != 2 {
		t.Fatalf("expected recovered buy followed by sell; creates=%d", f.creates)
	}
	if f.orders[102].Side != binance.SideTypeSell {
		t.Fatal("recovery duplicated the buy")
	}
	f.tick()
	if f.creates != 2 {
		t.Fatal("repeated recovery created another order")
	}
}

func TestUncertainSubmissionRemainsBlocked(t *testing.T) {
	f := newTradingFixture(t)
	f.rejectCreate = true
	f.tick()
	f.rejectCreate = false
	f.tick()
	if f.creates != 1 {
		t.Fatal("retried unresolved intent")
	}
}

func TestActivePartialBuyBlocksSell(t *testing.T) {
	f := newTradingFixture(t)
	f.seed(t, 1, "BUY", "PARTIALLY_FILLED", "10", "4", time.Minute)
	f.tick()
	if f.creates != 0 {
		t.Fatal("sold inventory while buy remained active")
	}
	if !f.position(t).Quantity.Equal(decimal.NewFromInt(4)) {
		t.Fatal("partial inventory lost")
	}
}

func TestStateBindingRejectsReuseAndLegacyPositions(t *testing.T) {
	f := newTradingFixture(t)
	f.svc.cfg.StateID = "account-a-demo"
	if err := f.svc.bindState(); err != nil {
		t.Fatal(err)
	}
	f.svc.cfg.StateID = "account-a-live"
	if err := f.svc.bindState(); err == nil {
		t.Fatal("accepted different environment")
	}
	delete(f.db.data, "metadata:identity")
	f.db.data["positions:BTCUSDT"] = []byte(`{"quantity":"1"}`)
	if err := f.svc.bindState(); err == nil {
		t.Fatal("accepted unversioned inventory")
	}
}

func TestReplayUsesExecutionCostAndRejectsOversell(t *testing.T) {
	f := newTradingFixture(t)
	f.seed(t, 1, "BUY", "FILLED", "10", "10", 3*time.Hour)
	f.seed(t, 2, "SELL", "CANCELED", "10", "4", 2*time.Hour)
	orders, err := f.svc.loadOrders("BTCUSDT")
	if err != nil {
		t.Fatal(err)
	}
	for i := range orders {
		if orders[i].Side == "BUY" {
			orders[i].QuoteQuantity = decimal.NewFromInt(900)
		}
	}
	pos, _, err := derivePosition("BTCUSDT", orders)
	if err != nil {
		t.Fatal(err)
	}
	if !pos.Quantity.Equal(decimal.NewFromInt(6)) || !pos.EntryPrice.Equal(decimal.NewFromInt(90)) {
		t.Fatalf("wrong remaining inventory/cost: %+v", pos)
	}
	orders[1].ExecutedQuantity = decimal.NewFromInt(11)
	if _, _, err := derivePosition("BTCUSDT", orders); err == nil {
		t.Fatal("accepted oversell")
	}
}

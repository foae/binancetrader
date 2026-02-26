package indicators

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockBulkResponse builds a JSON response with the given indicator data items.
func mockBulkResponse(items []bulkDataItem) []byte {
	resp := bulkResponse{Data: items}
	b, _ := json.Marshal(resp)
	return b
}

func allIndicatorData() map[string][]bulkDataItem {
	return map[string][]bulkDataItem{
		"1m": {
			{ID: "rsi", Result: map[string]any{"value": 55.5}},
			{ID: "roc", Result: map[string]any{"value": 2.35}},
			{ID: "mfi", Result: map[string]any{"value": 62.8}},
			{ID: "obv", Result: map[string]any{"value": 123456.0}},
		},
		"5m": {
			{ID: "dmi", Result: map[string]any{"adx": 28.3, "pdi": 22.1, "mdi": 15.7}},
			{ID: "natr", Result: map[string]any{"value": 0.42}},
			{ID: "macd", Result: map[string]any{"valueMACD": 1.5, "valueMACDSignal": 1.2, "valueMACDHist": 0.3}},
			{ID: "chop", Result: map[string]any{"value": 45.0}},
			{ID: "stochrsi", Result: map[string]any{"valueFastK": 0.75, "valueFastD": 0.68}},
		},
		"15m": {
			{ID: "supertrend", Result: map[string]any{"value": 95000.5, "valueAdvice": "long"}},
		},
	}
}

// requireFloat asserts a *float64 is non-nil and equals want.
func requireFloat(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %v", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}

// requireNilFloat asserts a *float64 is nil.
func requireNilFloat(t *testing.T, name string, got *float64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %v, want nil", name, *got)
	}
}

// intervalHandler returns an http.HandlerFunc that serves indicator data
// keyed by the request's construct interval.
func intervalHandler(t *testing.T, data map[string][]bulkDataItem) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var req bulkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		items, ok := data[req.Construct.Interval]
		if !ok {
			t.Fatalf("unexpected interval: %s", req.Construct.Interval)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(mockBulkResponse(items))
	}
}

func TestFetchSnapshot_Success(t *testing.T) {
	data := allIndicatorData()
	server := httptest.NewServer(intervalHandler(t, data))
	defer server.Close()

	c := NewClient("test-secret")
	c.endpoint = server.URL

	result, err := c.FetchSnapshot(context.Background(), "btc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FetchError != "" {
		t.Errorf("FetchError = %q, want empty", result.FetchError)
	}

	// All 10 indicators should be populated.
	// 1m
	requireFloat(t, "RSI", result.RSI, 55.5)
	requireFloat(t, "ROC", result.ROC, 2.35)
	requireFloat(t, "MFI", result.MFI, 62.8)
	requireFloat(t, "OBV", result.OBV, 123456.0)
	// 5m
	requireFloat(t, "ADX", result.ADX, 28.3)
	requireFloat(t, "PDI", result.PDI, 22.1)
	requireFloat(t, "MDI", result.MDI, 15.7)
	requireFloat(t, "NATR", result.NATR, 0.42)
	requireFloat(t, "MACDLine", result.MACDLine, 1.5)
	requireFloat(t, "MACDSignal", result.MACDSignal, 1.2)
	requireFloat(t, "MACDHistogram", result.MACDHistogram, 0.3)
	requireFloat(t, "CHOP", result.CHOP, 45.0)
	requireFloat(t, "StochRSIFastK", result.StochRSIFastK, 0.75)
	requireFloat(t, "StochRSIFastD", result.StochRSIFastD, 0.68)
	// 15m
	requireFloat(t, "SupertrendValue", result.SupertrendValue, 95000.5)
	if result.SupertrendAdvice == nil || *result.SupertrendAdvice != "long" {
		t.Errorf("SupertrendAdvice = %v, want \"long\"", result.SupertrendAdvice)
	}
}

func TestFetchSnapshot_CustomIndicators(t *testing.T) {
	data := allIndicatorData()
	server := httptest.NewServer(intervalHandler(t, data))
	defer server.Close()

	c := NewClientWithIndicators("test-secret", []IndicatorDef{RSI})
	c.endpoint = server.URL

	result, err := c.FetchSnapshot(context.Background(), "btc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FetchError != "" {
		t.Errorf("FetchError = %q, want empty", result.FetchError)
	}

	// Only RSI should be populated.
	requireFloat(t, "RSI", result.RSI, 55.5)

	// Everything else should be nil.
	requireNilFloat(t, "OBV", result.OBV)
	requireNilFloat(t, "ADX", result.ADX)
	requireNilFloat(t, "SupertrendValue", result.SupertrendValue)
}

func TestFetchSnapshot_PartialFailure(t *testing.T) {
	data := allIndicatorData()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req bulkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		// 5m interval fails
		if req.Construct.Interval == "5m" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "internal server error"}`))
			return
		}

		items := data[req.Construct.Interval]
		w.Header().Set("Content-Type", "application/json")
		w.Write(mockBulkResponse(items))
	}))
	defer server.Close()

	c := NewClient("test-secret")
	c.endpoint = server.URL

	result, err := c.FetchSnapshot(context.Background(), "btc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1m should be populated
	requireFloat(t, "RSI", result.RSI, 55.5)
	requireFloat(t, "ROC", result.ROC, 2.35)
	requireFloat(t, "MFI", result.MFI, 62.8)
	requireFloat(t, "OBV", result.OBV, 123456.0)

	// 15m should be populated
	requireFloat(t, "SupertrendValue", result.SupertrendValue, 95000.5)

	// 5m indicators should be nil (interval fetch failed)
	requireNilFloat(t, "ADX", result.ADX)
	requireNilFloat(t, "MACDLine", result.MACDLine)

	// FetchError should mention 5m
	if result.FetchError == "" {
		t.Fatal("FetchError should be set")
	}
	if !strings.Contains(result.FetchError, "5m") {
		t.Errorf("FetchError = %q, want it to contain '5m'", result.FetchError)
	}
}

func TestFetchSnapshot_AllFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "service unavailable"}`))
	}))
	defer server.Close()

	c := NewClient("test-secret")
	c.endpoint = server.URL

	result, err := c.FetchSnapshot(context.Background(), "btc")
	if err != nil {
		t.Fatalf("unexpected error (should not return error on fetch failures): %v", err)
	}

	// All values should be nil
	requireNilFloat(t, "RSI", result.RSI)
	requireNilFloat(t, "ROC", result.ROC)
	requireNilFloat(t, "MFI", result.MFI)
	requireNilFloat(t, "ADX", result.ADX)
	requireNilFloat(t, "SupertrendValue", result.SupertrendValue)

	if result.FetchError == "" {
		t.Fatal("FetchError should be set")
	}
	// Should mention all active intervals (1m, 5m, 15m)
	if !strings.Contains(result.FetchError, "1m") || !strings.Contains(result.FetchError, "5m") || !strings.Contains(result.FetchError, "15m") {
		t.Errorf("FetchError = %q, want mentions of 1m, 5m, and 15m", result.FetchError)
	}
}

func TestFetchSnapshot_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": "rate limit exceeded"}`))
	}))
	defer server.Close()

	c := NewClient("test-secret")
	c.endpoint = server.URL

	result, err := c.FetchSnapshot(context.Background(), "btc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FetchError == "" {
		t.Fatal("FetchError should be set for rate limiting")
	}
	if !strings.Contains(result.FetchError, "429") {
		t.Errorf("FetchError = %q, want mention of 429", result.FetchError)
	}
}

func TestFetchSnapshot_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second) // exceed timeout
	}))
	defer server.Close()

	c := NewClient("test-secret")
	c.endpoint = server.URL
	c.httpClient.Timeout = 100 * time.Millisecond // short timeout for test

	start := time.Now()
	result, err := c.FetchSnapshot(context.Background(), "btc")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FetchError == "" {
		t.Fatal("FetchError should be set for timeout")
	}

	// Should complete well within 2 seconds (3 timeouts of 100ms each + overhead)
	if elapsed > 2*time.Second {
		t.Errorf("fetch took %v, expected < 2s", elapsed)
	}
}

func TestAssetToSymbol(t *testing.T) {
	tests := []struct {
		asset   string
		want    string
		wantErr bool
	}{
		{"btc", "BTC/USDT", false},
		{"eth", "ETH/USDT", false},
		{"xrp", "XRP/USDT", false},
		{"sol", "SOL/USDT", false},
		{"BTC", "BTC/USDT", false}, // case insensitive
		{"unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.asset, func(t *testing.T) {
			got, err := assetToSymbol(tt.asset)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("assetToSymbol(%q) = %q, want %q", tt.asset, got, tt.want)
			}
		})
	}
}

func TestBuildPayloads(t *testing.T) {
	t.Run("default indicators", func(t *testing.T) {
		fetches := buildPayloads("test-secret", "BTC/USDT", DefaultIndicators)

		// DefaultIndicators spans 3 intervals: 1m (4), 5m (5), 15m (1)
		if len(fetches) != 3 {
			t.Fatalf("got %d interval fetches, want 3", len(fetches))
		}

		// Verify intervals and indicator counts.
		wantIntervals := []struct {
			interval string
			count    int
			ids      []string
		}{
			{"1m", 4, []string{"rsi", "obv", "roc", "mfi"}},
			{"5m", 5, []string{"dmi", "natr", "macd", "chop", "stochrsi"}},
			{"15m", 1, []string{"supertrend"}},
		}

		for i, want := range wantIntervals {
			f := fetches[i]
			if f.interval != want.interval {
				t.Errorf("fetches[%d].interval = %q, want %q", i, f.interval, want.interval)
			}
			if f.payload.Secret != "test-secret" {
				t.Errorf("fetches[%d].Secret = %q, want %q", i, f.payload.Secret, "test-secret")
			}
			if f.payload.Construct.Exchange != "binance" {
				t.Errorf("fetches[%d].Exchange = %q, want %q", i, f.payload.Construct.Exchange, "binance")
			}
			if f.payload.Construct.Symbol != "BTC/USDT" {
				t.Errorf("fetches[%d].Symbol = %q, want %q", i, f.payload.Construct.Symbol, "BTC/USDT")
			}
			if f.payload.Construct.Interval != want.interval {
				t.Errorf("fetches[%d].Construct.Interval = %q, want %q", i, f.payload.Construct.Interval, want.interval)
			}

			indicators := f.payload.Construct.Indicators
			if len(indicators) != want.count {
				t.Fatalf("fetches[%d]: got %d indicators, want %d", i, len(indicators), want.count)
			}
			for j, wantID := range want.ids {
				if indicators[j].ID != wantID {
					t.Errorf("fetches[%d].indicators[%d].ID = %q, want %q", i, j, indicators[j].ID, wantID)
				}
			}
		}
	})

	t.Run("single indicator", func(t *testing.T) {
		fetches := buildPayloads("s", "ETH/USDT", []IndicatorDef{RSI})

		if len(fetches) != 1 {
			t.Fatalf("got %d interval fetches, want 1", len(fetches))
		}
		if fetches[0].interval != "1m" {
			t.Errorf("interval = %q, want %q", fetches[0].interval, "1m")
		}
		if len(fetches[0].payload.Construct.Indicators) != 1 {
			t.Fatalf("got %d indicators, want 1", len(fetches[0].payload.Construct.Indicators))
		}
		if fetches[0].payload.Construct.Indicators[0].ID != "rsi" {
			t.Errorf("indicator ID = %q, want %q", fetches[0].payload.Construct.Indicators[0].ID, "rsi")
		}
	})

	t.Run("json roundtrip", func(t *testing.T) {
		fetches := buildPayloads("test-secret", "BTC/USDT", DefaultIndicators)
		for _, f := range fetches {
			b, err := json.Marshal(f.payload)
			if err != nil {
				t.Fatalf("failed to marshal payload: %v", err)
			}
			var decoded bulkRequest
			if err := json.Unmarshal(b, &decoded); err != nil {
				t.Fatalf("failed to unmarshal payload: %v", err)
			}
			if len(decoded.Construct.Indicators) != len(f.payload.Construct.Indicators) {
				t.Errorf("after round-trip: got %d indicators, want %d",
					len(decoded.Construct.Indicators), len(f.payload.Construct.Indicators))
			}
		}
	})
}

func TestFetchSnapshot_PerIndicatorError(t *testing.T) {
	// API returns 200 but individual indicators report errors.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req bulkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		var items []bulkDataItem
		switch req.Construct.Interval {
		case "1m":
			items = []bulkDataItem{
				{ID: "rsi", Result: map[string]any{"value": 55.5}},
				{ID: "obv", Result: map[string]any{"value": 123456.0}},
				{ID: "roc", Result: map[string]any{"value": 2.35}},
				// MFI has an error
				{ID: "mfi", Errors: []bulkError{{Error: "insufficient data"}}},
			}
		case "5m":
			items = allIndicatorData()["5m"]
		case "15m":
			items = allIndicatorData()["15m"]
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(mockBulkResponse(items))
	}))
	defer server.Close()

	c := NewClient("test-secret")
	c.endpoint = server.URL

	result, err := c.FetchSnapshot(context.Background(), "btc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// RSI and ROC should be populated (no error on those items).
	requireFloat(t, "RSI", result.RSI, 55.5)
	requireFloat(t, "ROC", result.ROC, 2.35)
	// MFI should be nil (item had error).
	requireNilFloat(t, "MFI", result.MFI)
	// 5m should be populated normally.
	requireFloat(t, "ADX", result.ADX, 28.3)

	// FetchError should mention the mfi indicator error.
	if result.FetchError == "" {
		t.Fatal("FetchError should be set for per-indicator errors")
	}
	if !strings.Contains(result.FetchError, "mfi") {
		t.Errorf("FetchError = %q, want it to contain 'mfi'", result.FetchError)
	}
	if !strings.Contains(result.FetchError, "insufficient data") {
		t.Errorf("FetchError = %q, want it to contain 'insufficient data'", result.FetchError)
	}
}

func TestFetchSnapshot_UnknownAsset(t *testing.T) {
	c := NewClient("test-secret")
	_, err := c.FetchSnapshot(context.Background(), "doge")
	if err == nil {
		t.Fatal("expected error for unknown asset")
	}
	if !strings.Contains(err.Error(), "unknown asset") {
		t.Errorf("error = %q, want it to contain 'unknown asset'", err.Error())
	}
}

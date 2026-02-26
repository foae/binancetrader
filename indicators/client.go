package indicators

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const bulkEndpoint = "https://api.taapi.io/bulk"

// Client wraps the taapi.io bulk API for fetching technical indicators.
type Client struct {
	secret     string
	httpClient *http.Client
	endpoint   string         // overridable for tests
	indicators []IndicatorDef // active indicator set
}

// NewClient creates a new taapi.io indicator client using DefaultIndicators.
func NewClient(secret string) *Client {
	return NewClientWithIndicators(secret, DefaultIndicators)
}

// NewClientWithIndicators creates a client with a custom indicator set.
func NewClientWithIndicators(secret string, indicators []IndicatorDef) *Client {
	return &Client{
		secret: secret,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		endpoint:   bulkEndpoint,
		indicators: indicators,
	}
}

// SetEndpoint overrides the API endpoint (for testing).
func (c *Client) SetEndpoint(url string) {
	c.endpoint = url
}

// intervalFetch groups indicators for a single interval into one bulk request.
type intervalFetch struct {
	interval string
	payload  bulkRequest
}

// FetchSnapshot makes one bulk request per active interval for the given asset
// and returns indicator values. Partial results are returned on partial failures
// (FetchError set). Never panics.
func (c *Client) FetchSnapshot(ctx context.Context, asset string) (*FetchResult, error) {
	symbol, err := assetToSymbol(asset)
	if err != nil {
		return nil, err
	}

	result := &FetchResult{}
	var fetchErrors []string

	fetches := buildPayloads(c.secret, symbol, c.indicators)

	for _, f := range fetches {
		resp, err := c.doRequest(ctx, f.payload)
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Sprintf("%s: %v", f.interval, err))
			continue
		}
		itemErrors := parseResponse(result, resp, c.indicators)
		for _, ie := range itemErrors {
			fetchErrors = append(fetchErrors, fmt.Sprintf("%s: %s", f.interval, ie))
		}
	}

	if len(fetchErrors) > 0 {
		result.FetchError = strings.Join(fetchErrors, "; ")
	}

	return result, nil
}

// assetToSymbol maps an asset identifier to a Binance trading pair.
func assetToSymbol(asset string) (string, error) {
	switch strings.ToLower(asset) {
	case "btc":
		return "BTC/USDT", nil
	case "eth":
		return "ETH/USDT", nil
	case "xrp":
		return "XRP/USDT", nil
	case "sol":
		return "SOL/USDT", nil
	default:
		return "", fmt.Errorf("unknown asset %q for indicator symbol mapping", asset)
	}
}

// buildPayloads groups indicators by interval and returns one bulk request per
// interval. Only intervals with active indicators are included.
func buildPayloads(secret, symbol string, defs []IndicatorDef) []intervalFetch {
	groups := make(map[string][]bulkIndicator)
	// Preserve deterministic ordering by tracking insertion order.
	var order []string
	for _, d := range defs {
		if _, exists := groups[d.Interval]; !exists {
			order = append(order, d.Interval)
		}
		groups[d.Interval] = append(groups[d.Interval], d.Params)
	}

	fetches := make([]intervalFetch, 0, len(groups))
	for _, interval := range order {
		fetches = append(fetches, intervalFetch{
			interval: interval,
			payload: bulkRequest{
				Secret: secret,
				Construct: bulkConstruct{
					Exchange:   "binance",
					Symbol:     symbol,
					Interval:   interval,
					Indicators: groups[interval],
				},
			},
		})
	}
	return fetches
}

// parseResponse maps response data by indicator id to FetchResult fields using
// the parse functions from the indicator definitions. Returns per-indicator errors.
func parseResponse(result *FetchResult, resp bulkResponse, defs []IndicatorDef) []string {
	// Build lookup from ID → parse function.
	parsers := make(map[string]func(*FetchResult, map[string]any), len(defs))
	for _, d := range defs {
		parsers[d.ID] = d.Parse
	}

	var itemErrors []string
	for _, item := range resp.Data {
		if len(item.Errors) > 0 {
			msgs := make([]string, len(item.Errors))
			for i, e := range item.Errors {
				msgs[i] = e.Error
			}
			itemErrors = append(itemErrors, fmt.Sprintf("indicator %s: %s", item.ID, strings.Join(msgs, ", ")))
			continue
		}

		if parse, ok := parsers[item.ID]; ok {
			parse(result, item.Result)
		}
	}
	return itemErrors
}

// doRequest sends a POST to the bulk endpoint and returns the parsed response.
func (c *Client) doRequest(ctx context.Context, payload bulkRequest) (bulkResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return bulkResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return bulkResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return bulkResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return bulkResponse{}, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return bulkResponse{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result bulkResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return bulkResponse{}, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result, nil
}

// floatVal extracts a float64 from a result map. Returns nil if the key is
// missing or the value is not a number.
func floatVal(m map[string]any, key string) *float64 {
	v, ok := m[key]
	if !ok {
		return nil
	}
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	return &f
}

// stringVal extracts a string from a result map. Returns nil if the key is
// missing or the value is not a string.
func stringVal(m map[string]any, key string) *string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}

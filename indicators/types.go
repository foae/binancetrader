package indicators

// FetchResult holds raw indicator values from a single taapi.io fetch.
// Pointer fields distinguish "not fetched / fetch failed" (nil) from a
// genuine API value of zero. The service layer adds metadata (asset, slug,
// tick number) before persisting as a storage.IndicatorTick.
type FetchResult struct {
	ADX *float64
	PDI *float64
	MDI *float64

	NATR *float64

	RSI *float64

	MACDLine      *float64
	MACDSignal    *float64
	MACDHistogram *float64

	SupertrendValue  *float64
	SupertrendAdvice *string // "long" or "short"

	OBV *float64

	ROC *float64 // Rate of Change (1m, period 9)
	MFI *float64 // Money Flow Index (1m, period 14)

	CHOP *float64

	StochRSIFastK *float64
	StochRSIFastD *float64

	FetchError string // non-empty if any interval request failed
}

// bulkRequest is the JSON payload sent to POST https://api.taapi.io/bulk.
type bulkRequest struct {
	Secret    string        `json:"secret"`
	Construct bulkConstruct `json:"construct"`
}

type bulkConstruct struct {
	Exchange   string          `json:"exchange"`
	Symbol     string          `json:"symbol"`
	Interval   string          `json:"interval"`
	Indicators []bulkIndicator `json:"indicators"`
}

type bulkIndicator struct {
	ID        string `json:"id"`
	Indicator string `json:"indicator"`

	// Optional parameters — omitted when zero-value.
	Period            int `json:"period,omitempty"`
	OptInFastPeriod   int `json:"optInFastPeriod,omitempty"`
	OptInSlowPeriod   int `json:"optInSlowPeriod,omitempty"`
	OptInSignalPeriod int `json:"optInSignalPeriod,omitempty"`
	KPeriod           int `json:"kPeriod,omitempty"`
	DPeriod           int `json:"dPeriod,omitempty"`
	RSIPeriod         int `json:"rsiPeriod,omitempty"`
	Multiplier        int `json:"multiplier,omitempty"`
}

// bulkResponse is the JSON response from the taapi.io bulk endpoint.
type bulkResponse struct {
	Data []bulkDataItem `json:"data"`
}

type bulkDataItem struct {
	ID     string         `json:"id"`
	Result map[string]any `json:"result"`
	Errors []bulkError    `json:"errors"`
}

type bulkError struct {
	Error string `json:"error"`
}

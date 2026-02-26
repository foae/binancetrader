package indicators

// IndicatorDef describes a single technical indicator for the taapi.io bulk API.
// The active set is composed from these definitions — add or remove entries from
// DefaultIndicators to change what gets fetched (one-line change).
type IndicatorDef struct {
	ID       string        // unique identifier (matches API response "id")
	Interval string        // "1m", "5m", "15m"
	Params   bulkIndicator // API request params
	Parse    func(*FetchResult, map[string]any)
}

// --- Full catalog (all known indicators) ---

var RSI = IndicatorDef{
	ID:       "rsi",
	Interval: "1m",
	Params:   bulkIndicator{ID: "rsi", Indicator: "rsi", Period: 14},
	Parse: func(r *FetchResult, m map[string]any) {
		r.RSI = floatVal(m, "value")
	},
}

var OBV = IndicatorDef{
	ID:       "obv",
	Interval: "1m",
	Params:   bulkIndicator{ID: "obv", Indicator: "obv"},
	Parse: func(r *FetchResult, m map[string]any) {
		r.OBV = floatVal(m, "value")
	},
}

var DMI = IndicatorDef{
	ID:       "dmi",
	Interval: "5m",
	Params:   bulkIndicator{ID: "dmi", Indicator: "dmi", Period: 14},
	Parse: func(r *FetchResult, m map[string]any) {
		r.ADX = floatVal(m, "adx")
		r.PDI = floatVal(m, "pdi")
		r.MDI = floatVal(m, "mdi")
	},
}

var NATR = IndicatorDef{
	ID:       "natr",
	Interval: "5m",
	Params:   bulkIndicator{ID: "natr", Indicator: "natr", Period: 14},
	Parse: func(r *FetchResult, m map[string]any) {
		r.NATR = floatVal(m, "value")
	},
}

var MACD = IndicatorDef{
	ID:       "macd",
	Interval: "5m",
	Params:   bulkIndicator{ID: "macd", Indicator: "macd", OptInFastPeriod: 12, OptInSlowPeriod: 26, OptInSignalPeriod: 9},
	Parse: func(r *FetchResult, m map[string]any) {
		r.MACDLine = floatVal(m, "valueMACD")
		r.MACDSignal = floatVal(m, "valueMACDSignal")
		r.MACDHistogram = floatVal(m, "valueMACDHist")
	},
}

var CHOP = IndicatorDef{
	ID:       "chop",
	Interval: "5m",
	Params:   bulkIndicator{ID: "chop", Indicator: "chop", Period: 14},
	Parse: func(r *FetchResult, m map[string]any) {
		r.CHOP = floatVal(m, "value")
	},
}

var StochRSI = IndicatorDef{
	ID:       "stochrsi",
	Interval: "5m",
	Params:   bulkIndicator{ID: "stochrsi", Indicator: "stochrsi", KPeriod: 14, DPeriod: 14, RSIPeriod: 14},
	Parse: func(r *FetchResult, m map[string]any) {
		r.StochRSIFastK = floatVal(m, "valueFastK")
		r.StochRSIFastD = floatVal(m, "valueFastD")
	},
}

var Supertrend = IndicatorDef{
	ID:       "supertrend",
	Interval: "15m",
	Params:   bulkIndicator{ID: "supertrend", Indicator: "supertrend", Period: 10, Multiplier: 3},
	Parse: func(r *FetchResult, m map[string]any) {
		r.SupertrendValue = floatVal(m, "value")
		r.SupertrendAdvice = stringVal(m, "valueAdvice")
	},
}

var ROC = IndicatorDef{
	ID:       "roc",
	Interval: "1m",
	Params:   bulkIndicator{ID: "roc", Indicator: "roc", Period: 9},
	Parse: func(r *FetchResult, m map[string]any) {
		r.ROC = floatVal(m, "value")
	},
}

var MFI = IndicatorDef{
	ID:       "mfi",
	Interval: "1m",
	Params:   bulkIndicator{ID: "mfi", Indicator: "mfi", Period: 14},
	Parse: func(r *FetchResult, m map[string]any) {
		r.MFI = floatVal(m, "value")
	},
}

// DefaultIndicators is the active set fetched by NewClient.
// Change this slice to add/remove indicators — one-line change.
var DefaultIndicators = []IndicatorDef{RSI, OBV, ROC, MFI, DMI, NATR, MACD, CHOP, StochRSI, Supertrend}

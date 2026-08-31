package domain

// Symbol is a provider-native identifier for a tradable market, spelled
// exactly as the Provider spells it (for example "BTCUSDT").
type Symbol string

// String returns the Symbol as the Provider spells it.
func (s Symbol) String() string { return string(s) }

// DatasetID uniquely identifies a Dataset: the bars one Provider returned for
// one Symbol at one Timeframe.
type DatasetID struct {
	Provider  string
	Symbol    Symbol
	Timeframe Timeframe
}

// String renders the Dataset as provider/symbol/timeframe, the form used in
// URLs and logs.
func (d DatasetID) String() string {
	return d.Provider + "/" + string(d.Symbol) + "/" + string(d.Timeframe)
}

package playground

import "net/http"

// Operation is one domain route as the playground offers it: what it is
// called, how to reach it, what it takes, and how the same thing is spelled
// on the command line.
//
// The catalog is hand-written rather than generated. Ten routes is few enough
// to spell out, and a generator would have to invent the names, the examples
// and the CLI templates anyway — none of which the mux knows.
type Operation struct {
	Name   string  `json:"name"`
	Method string  `json:"method"`
	Path   string  `json:"path"`
	Params []Param `json:"params"`

	// CLI is the equivalent command, with {param} where a value goes, or nil
	// where the CLI has no such command. The syntax is the CLI's real one:
	// positional arguments, and only the flags it really has.
	CLI *string `json:"cli"`
}

// Param is one value an Operation takes: In says where it goes — "path",
// "query" or "body" — and Example is a value that works, so a form can be
// filled in and executed without knowing anything.
type Param struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
	Example  string `json:"example"`
}

// The three places a Param goes.
const (
	inPath  = "path"
	inQuery = "query"
	inBody  = "body"
)

// cli returns a pointer to the template, so the JSON field is a string. The
// two Operations with no command leave the field nil, which is null.
func cli(template string) *string { return &template }

// The examples every Operation is filled in with. One Dataset — the Binance
// BTCUSDT minute bars of 2024-01-01 — so a reader following the catalog top
// to bottom keeps asking about the same thing.
const (
	exampleProvider   = "binance"
	exampleSymbol     = "BTCUSDT"
	exampleTimeframe  = "1m"
	exampleStart      = "2024-01-01"
	exampleEnd        = "2024-01-02"
	exampleBackfillID = "8f14e45fce7a4f0e9b2c9dd3ab5e7a71"
	exampleGapID      = "1"
	exampleGapStatus  = "ignored"
	exampleGapReason  = "known outage"
	exampleFormat     = "json"
)

// dataset is the {provider}/{symbol}/{timeframe} prefix every Dataset route
// carries, which is three path Params spelled the same way each time.
func dataset() []Param {
	return []Param{
		{Name: "provider", In: inPath, Required: true, Example: exampleProvider},
		{Name: "symbol", In: inPath, Required: true, Example: exampleSymbol},
		{Name: "timeframe", In: inPath, Required: true, Example: exampleTimeframe},
	}
}

// rangeQuery is the ?start&end pair the two range questions take.
func rangeQuery() []Param {
	return []Param{
		{Name: "start", In: inQuery, Required: true, Example: exampleStart},
		{Name: "end", In: inQuery, Required: true, Example: exampleEnd},
	}
}

// Operations is the catalog: the ten domain routes, in the order
// httpapi.New registers them.
var Operations = []Operation{
	{
		Name:   "List Providers",
		Method: http.MethodGet,
		Path:   "/providers",
		Params: []Param{},
		CLI:    cli("agnoforge data providers"),
	},
	{
		Name:   "Start a Backfill",
		Method: http.MethodPost,
		Path:   "/backfills",
		Params: []Param{
			{Name: "provider", In: inBody, Required: true, Example: exampleProvider},
			{Name: "symbol", In: inBody, Required: true, Example: exampleSymbol},
			{Name: "timeframe", In: inBody, Required: true, Example: exampleTimeframe},
			{Name: "start", In: inBody, Required: true, Example: exampleStart},
			{Name: "end", In: inBody, Required: true, Example: exampleEnd},
		},
		CLI: cli("agnoforge data backfill {provider} {symbol} {timeframe} {start} {end}"),
	},
	{
		Name:   "Show a Backfill",
		Method: http.MethodGet,
		Path:   "/backfills/{id}",
		Params: []Param{
			{Name: "id", In: inPath, Required: true, Example: exampleBackfillID},
		},
		CLI: cli("agnoforge data status {id}"),
	},
	{
		Name:   "Cancel a Backfill",
		Method: http.MethodDelete,
		Path:   "/backfills/{id}",
		Params: []Param{
			{Name: "id", In: inPath, Required: true, Example: exampleBackfillID},
		},
		CLI: cli("agnoforge data cancel {id}"),
	},
	{
		Name:   "Show a Dataset's Coverage",
		Method: http.MethodGet,
		Path:   "/datasets/{provider}/{symbol}/{timeframe}/coverage",
		Params: dataset(),
		// No CLI command asks for Coverage.
		CLI: nil,
	},
	{
		Name:   "Ask whether a range is Complete",
		Method: http.MethodGet,
		Path:   "/datasets/{provider}/{symbol}/{timeframe}/complete",
		Params: append(dataset(), rangeQuery()...),
		CLI:    cli("agnoforge data complete {provider} {symbol} {timeframe} {start} {end}"),
	},
	{
		Name:   "List a Dataset's Gaps",
		Method: http.MethodGet,
		Path:   "/datasets/{provider}/{symbol}/{timeframe}/gaps",
		Params: append(dataset(),
			Param{Name: "status", In: inQuery, Required: false, Example: exampleGapStatus}),
		CLI: cli("agnoforge data gaps {provider} {symbol} {timeframe} -status {status}"),
	},
	{
		Name:   "Query a Dataset's Bars",
		Method: http.MethodGet,
		Path:   "/datasets/{provider}/{symbol}/{timeframe}/bars",
		Params: append(append(dataset(), rangeQuery()...),
			Param{Name: "format", In: inQuery, Required: false, Example: exampleFormat}),
		CLI: cli("agnoforge data query {provider} {symbol} {timeframe} {start} {end} -o bars.parquet -format {format}"),
	},
	{
		Name:   "Settle a Gap",
		Method: http.MethodPatch,
		Path:   "/gaps/{id}",
		Params: []Param{
			{Name: "id", In: inPath, Required: true, Example: exampleGapID},
			{Name: "status", In: inBody, Required: true, Example: exampleGapStatus},
			{Name: "reason", In: inBody, Required: false, Example: exampleGapReason},
		},
		// No CLI command sets a Gap's status.
		CLI: nil,
	},
	{
		Name:   "Repair a Gap",
		Method: http.MethodPost,
		Path:   "/gaps/{id}/repair",
		Params: []Param{
			{Name: "id", In: inPath, Required: true, Example: exampleGapID},
		},
		CLI: cli("agnoforge data repair {id}"),
	},
}

// listOperations answers GET /playground/operations with the catalog. It is a
// package-level function, not a method: the catalog is static, and knows
// neither the trace store nor anything the service does.
func listOperations(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Operations)
}

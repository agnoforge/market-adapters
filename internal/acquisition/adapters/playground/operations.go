package playground

import "net/http"

// Operation is one domain route as the playground offers it: what it is
// called, how to reach it, what it takes, and how the same thing is spelled
// on the command line.
//
// The catalog is hand-written rather than generated. This many routes is few
// enough to spell out, and a generator would have to invent the names, the
// examples and the CLI templates anyway — none of which the mux knows.
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
	// The Composite Dataset the composite operations ask about, and the derived
	// timeframe a query reads: the same market as the Dataset above, assembled.
	exampleComposite          = "btc-usd"
	exampleCompositeTimeframe = "1h"
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

// composite is the {name} path param every Composite Dataset route carries.
func composite() []Param {
	return []Param{
		{Name: "name", In: inPath, Required: true, Example: exampleComposite},
	}
}

// Operations is the catalog: the ten acquisition routes in the order
// httpapi.New registers them, then the Composite Dataset routes in the order
// the composite adapter registers them.
//
// The two routes that declare a Composite Dataset — POST /composites and PUT
// /composites/{name} — are deliberately not here. Their body is a nested
// document (a base source, a catch-up source, a list of timeframes), and a
// Param is one flat value: an entry for them could be listed but never
// executed, which is the one thing this catalog promises. They are spelled in
// the README and on the command line (`agnoforge composite create …`), and
// everything a declared dataset can then do is below.
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
	{
		Name:   "List Composite Datasets",
		Method: http.MethodGet,
		Path:   "/composites",
		Params: []Param{},
		CLI:    cli("agnoforge composite list"),
	},
	{
		Name:   "Show a Composite Dataset",
		Method: http.MethodGet,
		Path:   "/composites/{name}",
		Params: composite(),
		CLI:    cli("agnoforge composite get {name}"),
	},
	{
		Name:   "Delete a Composite Dataset",
		Method: http.MethodDelete,
		Path:   "/composites/{name}",
		Params: composite(),
		CLI:    cli("agnoforge composite delete {name}"),
	},
	{
		Name:   "Build a Composite Dataset",
		Method: http.MethodPost,
		Path:   "/composites/{name}/build",
		Params: composite(),
		CLI:    cli("agnoforge composite build {name}"),
	},
	{
		Name:   "Query a Composite Dataset's Bars",
		Method: http.MethodGet,
		Path:   "/composites/{name}/bars",
		// Every bound is optional here: omitting them asks for the dataset's own
		// resolved range, and omitting the timeframe asks for the 1m composite
		// timeline.
		Params: append(composite(),
			Param{Name: "timeframe", In: inQuery, Required: false, Example: exampleCompositeTimeframe},
			Param{Name: "start", In: inQuery, Required: false, Example: exampleStart},
			Param{Name: "end", In: inQuery, Required: false, Example: exampleEnd},
			Param{Name: "format", In: inQuery, Required: false, Example: exampleFormat}),
		CLI: cli("agnoforge composite query {name} -o bars.parquet -timeframe {timeframe} -start {start} -end {end} -format {format}"),
	},
	{
		Name:   "Show a Composite Dataset's Quality",
		Method: http.MethodGet,
		Path:   "/composites/{name}/quality",
		Params: composite(),
		CLI:    cli("agnoforge composite quality {name}"),
	},
}

// listOperations answers GET /playground/operations with the catalog. It is a
// package-level function, not a method: the catalog is static, and knows
// neither the trace store nor anything the service does.
func listOperations(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Operations)
}

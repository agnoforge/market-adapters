package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// This file is the client half of the composites resource: one subcommand per
// route, each of them a thin HTTP client exactly like the data commands. It
// knows the route it calls and the flags that spell a declaration, and
// nothing else — every rule about a configuration is the service's.

// compositeSourceJSON names one source Dataset in a declaration.
type compositeSourceJSON struct {
	Instrument string `json:"instrument,omitempty"`
	Provider   string `json:"provider"`
	Symbol     string `json:"symbol"`
	Timeframe  string `json:"timeframe"`
}

// compositeCatchUpJSON declares how the tail is filled.
type compositeCatchUpJSON struct {
	Kind       string `json:"kind"`
	Instrument string `json:"instrument,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Symbol     string `json:"symbol,omitempty"`
	Timeframe  string `json:"timeframe,omitempty"`
}

// compositeConfigJSON is the declaration, the body of a create and of an edit
// alike.
type compositeConfigJSON struct {
	Instrument     string                `json:"instrument"`
	Base           compositeSourceJSON   `json:"base"`
	CatchUp        *compositeCatchUpJSON `json:"catch_up,omitempty"`
	RequestedStart string                `json:"requested_start"`
	RequestedEnd   string                `json:"requested_end"`
	Timeframes     []string              `json:"timeframes"`
	Mode           string                `json:"mode,omitempty"`
}

// compositeCreateJSON is the body of POST /composites.
type compositeCreateJSON struct {
	Name string `json:"name"`
	compositeConfigJSON
}

// runComposite dispatches the composite subcommands. Every one of them maps to
// exactly one route.
func runComposite(args []string, stdout, stderr io.Writer, env func(string) string) int {
	if len(args) == 0 {
		return usageError(stderr, "composite needs a command")
	}
	ctx := context.Background()
	c := newClient(env)
	switch args[0] {
	case "create":
		return compositeCreate(ctx, c, args[1:], stdout, stderr)
	case "list":
		return compositeList(ctx, c, args[1:], stdout, stderr)
	case "get":
		return compositeGet(ctx, c, args[1:], stdout, stderr)
	case "build":
		return compositeBuild(ctx, c, args[1:], stdout, stderr)
	case "query":
		return compositeQuery(ctx, c, args[1:], stdout, stderr)
	case "quality":
		return compositeQuality(ctx, c, args[1:], stdout, stderr)
	case "edit":
		return compositeEdit(ctx, c, args[1:], stdout, stderr)
	case "delete":
		return compositeDelete(ctx, c, args[1:], stdout, stderr)
	default:
		return usageError(stderr, fmt.Sprintf("unknown composite command %q", args[0]))
	}
}

// compositePath spells one composites route.
func compositePath(name string) string { return "/composites/" + url.PathEscape(name) }

// declarationFlags are the flags that spell a configuration. create and edit
// take the same ones, because an edit replaces the whole declaration.
type declarationFlags struct {
	instrument *string
	base       *string
	catchUp    *string
	start      *string
	end        *string
	timeframes *string
	mode       *string
}

func newDeclarationFlags(fs *flag.FlagSet) *declarationFlags {
	return &declarationFlags{
		instrument: fs.String("instrument", "", "the logical market, e.g. BTC/USD"),
		base:       fs.String("base", "", "the base 1m source as provider:symbol, e.g. binance:BTCUSDT"),
		catchUp:    fs.String("catch-up", "", `"none", or a provider:symbol to continue past the base provider; empty means the base provider`),
		start:      fs.String("start", "", "the requested start, an RFC3339 instant or a YYYY-MM-DD date"),
		end:        fs.String("end", "", `the requested end, an instant or the literal "now"`),
		timeframes: fs.String("timeframes", "", "the timeframes to materialize, comma separated, e.g. 5m,1h,1d"),
		mode:       fs.String("mode", "", `"strict" (default) or "research"`),
	}
}

// config builds the declaration the flags spell. Only what the client cannot
// send at all is refused here; everything else is the service's judgement.
func (d *declarationFlags) config(stderr io.Writer) (compositeConfigJSON, int, bool) {
	base, ok := parseSourceFlag(*d.base)
	if !ok {
		return compositeConfigJSON{}, usageError(stderr, "-base must be provider:symbol, e.g. binance:BTCUSDT"), false
	}
	cfg := compositeConfigJSON{
		Instrument:     *d.instrument,
		Base:           compositeSourceJSON{Provider: base[0], Symbol: base[1], Timeframe: "1m"},
		RequestedStart: *d.start,
		RequestedEnd:   *d.end,
		Timeframes:     splitList(*d.timeframes),
		Mode:           *d.mode,
	}
	switch {
	case *d.catchUp == "":
	case *d.catchUp == "none":
		cfg.CatchUp = &compositeCatchUpJSON{Kind: "none"}
	default:
		source, ok := parseSourceFlag(*d.catchUp)
		if !ok {
			return compositeConfigJSON{}, usageError(stderr, `-catch-up must be "none" or provider:symbol`), false
		}
		cfg.CatchUp = &compositeCatchUpJSON{
			Kind: "source", Provider: source[0], Symbol: source[1], Timeframe: "1m",
		}
	}
	return cfg, exitOK, true
}

// parseSourceFlag reads a provider:symbol pair.
func parseSourceFlag(v string) ([2]string, bool) {
	provider, symbol, found := strings.Cut(v, ":")
	if !found || provider == "" || symbol == "" {
		return [2]string{}, false
	}
	return [2]string{provider, symbol}, true
}

// splitList reads a comma-separated flag. An empty flag is an empty list, not
// a list holding the empty string.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return []string{}
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// compositeCreate answers POST /composites.
func compositeCreate(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite create", stderr)
	flags := newDeclarationFlags(fs)
	positional, code, ok := parseArgs(fs, args, 1, "composite create <name> -instrument <market> -base <provider:symbol> -start <t> -end <t|now> [-catch-up none|provider:symbol] [-timeframes 5m,1h] [-mode strict|research]", stderr)
	if !ok {
		return code
	}
	cfg, code, ok := flags.config(stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodPost, "/composites", nil,
		compositeCreateJSON{Name: positional[0], compositeConfigJSON: cfg})
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// compositeList answers GET /composites.
func compositeList(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite list", stderr)
	if _, code, ok := parseArgs(fs, args, 0, "composite list", stderr); !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodGet, "/composites", nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// compositeGet answers GET /composites/{name}.
func compositeGet(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite get", stderr)
	positional, code, ok := parseArgs(fs, args, 1, "composite get <name>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodGet, compositePath(positional[0]), nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// compositeBuild answers POST /composites/{name}/build: reconcile the
// declaration against the source data that exists. It prints the built
// dataset — segments and quality included — and fails loudly when the build
// could not leave the dataset ready.
func compositeBuild(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite build", stderr)
	positional, code, ok := parseArgs(fs, args, 1, "composite build <name>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodPost, compositePath(positional[0])+"/build", nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// compositeEdit answers PUT /composites/{name}. The body is a whole
// declaration: an edit replaces the configuration, it does not patch it.
func compositeEdit(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite edit", stderr)
	flags := newDeclarationFlags(fs)
	positional, code, ok := parseArgs(fs, args, 1, "composite edit <name> -instrument <market> -base <provider:symbol> -start <t> -end <t|now> [-catch-up none|provider:symbol] [-timeframes 5m,1h] [-mode strict|research]", stderr)
	if !ok {
		return code
	}
	cfg, code, ok := flags.config(stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodPut, compositePath(positional[0]), nil, cfg)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// compositeQuery answers
// GET /composites/{name}/bars?timeframe&start&end[&format=json]: the bars of a
// Composite Dataset, streamed where -o says.
//
// It is spelled the way `data query` is — the body goes to -o, Parquet unless
// -format json — with one difference the composite side earns: both bounds are
// optional, and omitting them asks for the dataset's own resolved range. What
// the dataset is when it answered is printed first, so a stale or research
// dataset is visibly one even when the body is a Parquet stream.
func compositeQuery(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite query", stderr)
	timeframe := fs.String("timeframe", "", `the timeframe to read, e.g. 1h; empty is the 1m composite timeline`)
	start := fs.String("start", "", "the start of the range; empty is the dataset's requested start")
	end := fs.String("end", "", "the end of the range; empty is the dataset's resolved end")
	format := fs.String("format", "", `"json" for the JSON bars instead of Parquet`)
	out := fs.String("o", "", "file to write the response body to, - for stdout")
	positional, code, ok := parseArgs(fs, args, 1,
		"composite query <name> -o <file|-> [-timeframe 1h] [-start t] [-end t] [-format json]", stderr)
	if !ok {
		return code
	}
	if *out == "" {
		return usageError(stderr, "composite query needs -o <file> (- for stdout)")
	}
	query := url.Values{}
	for name, value := range map[string]string{
		"timeframe": *timeframe, "start": *start, "end": *end, "format": *format,
	} {
		if value != "" {
			query.Set(name, value)
		}
	}
	res, err := c.do(ctx, http.MethodGet, compositePath(positional[0])+"/bars", query, nil)
	if err != nil {
		return failure(stderr, err)
	}
	defer res.Body.Close()

	fmt.Fprintf(stdout, "state: %s\n", header(res, "X-Composite-State"))
	fmt.Fprintf(stdout, "mode: %s\n", header(res, "X-Composite-Mode"))
	fmt.Fprintf(stdout, "timeframe: %s\n", header(res, "X-Timeframe"))
	fmt.Fprintf(stdout, "range: %s .. %s\n", header(res, "X-Range-Start"), header(res, "X-Range-End"))

	written, err := writeBody(*out, res.Body, stdout)
	if err != nil {
		return failure(stderr, err)
	}
	if *out != "-" {
		fmt.Fprintf(stdout, "wrote %d bytes to %s\n", written, *out)
	}
	return exitOK
}

// header reads one response header, or "unknown" when the service did not send
// it — the way `data query` reports a missing X-Complete.
func header(res *http.Response, name string) string {
	if value := res.Header.Get(name); value != "" {
		return value
	}
	return "unknown"
}

// compositeQuality answers GET /composites/{name}/quality: the Quality the
// last Build persisted, with the state and mode it belongs to.
func compositeQuality(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite quality", stderr)
	positional, code, ok := parseArgs(fs, args, 1, "composite quality <name>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodGet, compositePath(positional[0])+"/quality", nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// compositeDelete answers DELETE /composites/{name}.
func compositeDelete(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("composite delete", stderr)
	positional, code, ok := parseArgs(fs, args, 1, "composite delete <name>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodDelete, compositePath(positional[0]), nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

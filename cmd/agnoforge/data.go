package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// defaultServiceURL is where a service started by `agnoforge serve` with the
// default AGNOFORGE_LISTEN answers.
const defaultServiceURL = "http://localhost:8080"

// client is the whole HTTP client this CLI has: one method per verb, no
// knowledge of any route beyond the one its caller names.
type client struct {
	base string
	http *http.Client
}

// newClient builds a client for the service AGNOFORGE_URL names.
func newClient(env func(string) string) *client {
	base := env("AGNOFORGE_URL")
	if base == "" {
		base = defaultServiceURL
	}
	return &client{
		base: strings.TrimSuffix(base, "/"),
		// A Backfill answers immediately, but a Parquet export of a large
		// range streams for a while, so the timeout is generous.
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

// do performs one request and hands back the still-open response. A non-2xx
// status is an error carrying the service's own message, and its body is
// closed before returning.
func (c *client) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		defer res.Body.Close()
		return nil, &serviceError{
			message: serviceMessage(res),
			traceID: res.Header.Get(traceIDHeader),
		}
	}
	return res, nil
}

// serviceError is a non-2xx: the message the service reported, and the trace
// it happened in when the service is one that traces. Only a response can
// carry a trace id — a request that never arrived has none.
type serviceError struct {
	message string
	traceID string
}

func (e *serviceError) Error() string { return e.message }

// bytes performs one request and returns the whole body.
func (c *client) bytes(ctx context.Context, method, path string, query url.Values, body any) ([]byte, error) {
	res, err := c.do(ctx, method, path, query, body)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

// serviceMessage reads the failure the service reported: the {"error": …}
// document every route answers with, falling back to the status line when the
// body is not one.
func serviceMessage(res *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var document struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &document); err == nil && document.Error != "" {
		return document.Error
	}
	return strings.TrimSpace(res.Status)
}

// datasetPath spells one Dataset sub-resource route.
func datasetPath(provider, symbol, timeframe, resource string) string {
	return "/datasets/" + url.PathEscape(provider) +
		"/" + url.PathEscape(symbol) +
		"/" + url.PathEscape(timeframe) +
		"/" + resource
}

// printJSON writes a response the way a person reads it. A body that is not
// JSON is printed as it came.
func printJSON(stdout io.Writer, raw []byte) int {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		fmt.Fprintf(stdout, "%s", raw)
		return exitOK
	}
	fmt.Fprintf(stdout, "%s\n", strings.TrimRight(pretty.String(), "\n"))
	return exitOK
}

// runData dispatches the client subcommands. Every one of them maps to
// exactly one route.
func runData(args []string, stdout, stderr io.Writer, env func(string) string) int {
	if len(args) == 0 {
		return usageError(stderr, "data needs a command")
	}
	ctx := context.Background()
	c := newClient(env)
	switch args[0] {
	case "providers":
		return dataProviders(ctx, c, args[1:], stdout, stderr)
	case "backfill":
		return dataBackfill(ctx, c, args[1:], stdout, stderr)
	case "status":
		return dataStatus(ctx, c, args[1:], stdout, stderr)
	case "cancel":
		return dataCancel(ctx, c, args[1:], stdout, stderr)
	case "gaps":
		return dataGaps(ctx, c, args[1:], stdout, stderr)
	case "repair":
		return dataRepair(ctx, c, args[1:], stdout, stderr)
	case "complete":
		return dataComplete(ctx, c, args[1:], stdout, stderr)
	case "query":
		return dataQuery(ctx, c, args[1:], stdout, stderr)
	default:
		return usageError(stderr, fmt.Sprintf("unknown data command %q", args[0]))
	}
}

// flagsFor builds the FlagSet of one subcommand. ContinueOnError keeps a bad
// flag from calling os.Exit, so run stays testable in-process.
func flagsFor(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseArgs reads a subcommand's arguments: the positional ones first, then
// its flags. The flag package stops at the first non-flag argument, so the
// two groups are split before parsing — which is what lets a command read as
// `data gaps binance BTCUSDT 1m -status open`.
//
// It reports the positional arguments and, when they are not exactly want,
// the usage exit code instead.
func parseArgs(fs *flag.FlagSet, args []string, want int, spelling string, stderr io.Writer) ([]string, int, bool) {
	positional, flags := splitArgs(args)
	if err := fs.Parse(flags); err != nil {
		return nil, usageError(stderr, ""), false
	}
	positional = append(positional, fs.Args()...)
	if len(positional) != want {
		return nil, usageError(stderr, fmt.Sprintf("%s takes %d arguments, got %d: %s", fs.Name(), want, len(positional), spelling)), false
	}
	return positional, exitOK, true
}

// splitArgs separates the leading positional arguments from the flags that
// follow them. A bare "-" is a positional (it is how query names stdout).
func splitArgs(args []string) (positional, flags []string) {
	for i, a := range args {
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			return args[:i], args[i:]
		}
	}
	return args, nil
}

// dataProviders answers GET /providers.
func dataProviders(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data providers", stderr)
	if _, code, ok := parseArgs(fs, args, 0, "data providers", stderr); !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodGet, "/providers", nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// backfillRequestJSON is the body of POST /backfills. The two instants are
// passed through verbatim: the service reads an RFC3339 instant or a
// YYYY-MM-DD date, and this client does not need to know which it was given.
type backfillRequestJSON struct {
	Provider  string `json:"provider"`
	Symbol    string `json:"symbol"`
	Timeframe string `json:"timeframe"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

// backfillStartedJSON is the 202 of POST /backfills.
type backfillStartedJSON struct {
	ID             string `json:"id"`
	EffectiveRange struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"effective_range"`
}

// backfillJSON is what GET /backfills/{id} reports, reduced to the fields
// -wait needs to decide it is finished.
type backfillJSON struct {
	ID             string `json:"id"`
	State          string `json:"state"`
	BarsDownloaded int64  `json:"bars_downloaded"`
	LastError      string `json:"last_error"`
}

// terminal reports whether a Backfill state is one it can no longer leave.
func terminal(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled"
}

// dataBackfill answers POST /backfills, and with -wait polls
// GET /backfills/{id} until the Backfill is in a terminal state.
func dataBackfill(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data backfill", stderr)
	wait := fs.Bool("wait", false, "poll until the backfill reaches a terminal state")
	interval := fs.Duration("interval", 200*time.Millisecond, "how often -wait polls")
	positional, code, ok := parseArgs(fs, args, 5, "data backfill <provider> <symbol> <timeframe> <start> <end>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodPost, "/backfills", nil, backfillRequestJSON{
		Provider:  positional[0],
		Symbol:    positional[1],
		Timeframe: positional[2],
		Start:     positional[3],
		End:       positional[4],
	})
	if err != nil {
		return failure(stderr, err)
	}
	var started backfillStartedJSON
	if err := json.Unmarshal(raw, &started); err != nil {
		return failure(stderr, fmt.Errorf("decoding response: %w", err))
	}
	fmt.Fprintf(stdout, "id: %s\n", started.ID)
	fmt.Fprintf(stdout, "effective range: %s .. %s\n", started.EffectiveRange.Start, started.EffectiveRange.End)
	if !*wait {
		return exitOK
	}
	return waitForBackfill(ctx, c, started.ID, *interval, stdout, stderr)
}

// waitForBackfill polls one Backfill until it is finished and reports how it
// ended. A failed Backfill is a failed command.
func waitForBackfill(ctx context.Context, c *client, id string, interval time.Duration, stdout, stderr io.Writer) int {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	for {
		raw, err := c.bytes(ctx, http.MethodGet, "/backfills/"+url.PathEscape(id), nil, nil)
		if err != nil {
			return failure(stderr, err)
		}
		var status backfillJSON
		if err := json.Unmarshal(raw, &status); err != nil {
			return failure(stderr, fmt.Errorf("decoding response: %w", err))
		}
		if terminal(status.State) {
			fmt.Fprintf(stdout, "state: %s\n", status.State)
			fmt.Fprintf(stdout, "bars downloaded: %d\n", status.BarsDownloaded)
			if status.State == "failed" {
				return failure(stderr, fmt.Errorf("backfill %s failed: %s", id, status.LastError))
			}
			return exitOK
		}
		time.Sleep(interval)
	}
}

// dataStatus answers GET /backfills/{id}.
func dataStatus(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data status", stderr)
	positional, code, ok := parseArgs(fs, args, 1, "data status <backfill-id>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodGet, "/backfills/"+url.PathEscape(positional[0]), nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// dataCancel answers DELETE /backfills/{id}.
func dataCancel(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data cancel", stderr)
	positional, code, ok := parseArgs(fs, args, 1, "data cancel <backfill-id>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodDelete, "/backfills/"+url.PathEscape(positional[0]), nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// dataGaps answers GET /datasets/{p}/{s}/{tf}/gaps, narrowed by -status.
func dataGaps(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data gaps", stderr)
	status := fs.String("status", "", "only gaps with this status")
	positional, code, ok := parseArgs(fs, args, 3, "data gaps <provider> <symbol> <timeframe>", stderr)
	if !ok {
		return code
	}
	query := url.Values{}
	if *status != "" {
		query.Set("status", *status)
	}
	raw, err := c.bytes(ctx, http.MethodGet, datasetPath(positional[0], positional[1], positional[2], "gaps"), query, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// dataRepair answers POST /gaps/{id}/repair.
func dataRepair(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data repair", stderr)
	positional, code, ok := parseArgs(fs, args, 1, "data repair <gap-id>", stderr)
	if !ok {
		return code
	}
	raw, err := c.bytes(ctx, http.MethodPost, "/gaps/"+url.PathEscape(positional[0])+"/repair", nil, nil)
	if err != nil {
		return failure(stderr, err)
	}
	return printJSON(stdout, raw)
}

// dataComplete answers GET /datasets/{p}/{s}/{tf}/complete?start&end. It is a
// query, so the verdict is on the first line and the exit code is 0 either
// way — only a failed request is non-zero.
func dataComplete(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data complete", stderr)
	positional, code, ok := parseArgs(fs, args, 5, "data complete <provider> <symbol> <timeframe> <start> <end>", stderr)
	if !ok {
		return code
	}
	query := url.Values{"start": {positional[3]}, "end": {positional[4]}}
	raw, err := c.bytes(ctx, http.MethodGet, datasetPath(positional[0], positional[1], positional[2], "complete"), query, nil)
	if err != nil {
		return failure(stderr, err)
	}
	var verdict struct {
		Complete bool `json:"complete"`
	}
	if err := json.Unmarshal(raw, &verdict); err != nil {
		return failure(stderr, fmt.Errorf("decoding response: %w", err))
	}
	fmt.Fprintf(stdout, "complete: %t\n", verdict.Complete)
	return printJSON(stdout, raw)
}

// dataQuery answers GET /datasets/{p}/{s}/{tf}/bars?start&end[&format=json].
// The body is written where -o says, and the X-Complete verdict is printed
// first — a range is flagged, never refused.
func dataQuery(ctx context.Context, c *client, args []string, stdout, stderr io.Writer) int {
	fs := flagsFor("data query", stderr)
	out := fs.String("o", "", "file to write the response body to, - for stdout")
	format := fs.String("format", "", `"json" for the JSON bars instead of Parquet`)
	positional, code, ok := parseArgs(fs, args, 5, "data query <provider> <symbol> <timeframe> <start> <end> -o <file>", stderr)
	if !ok {
		return code
	}
	if *out == "" {
		return usageError(stderr, "data query needs -o <file> (- for stdout)")
	}
	query := url.Values{"start": {positional[3]}, "end": {positional[4]}}
	if *format != "" {
		query.Set("format", *format)
	}
	res, err := c.do(ctx, http.MethodGet, datasetPath(positional[0], positional[1], positional[2], "bars"), query, nil)
	if err != nil {
		return failure(stderr, err)
	}
	defer res.Body.Close()

	complete := res.Header.Get("X-Complete")
	if complete == "" {
		complete = "unknown"
	}
	fmt.Fprintf(stdout, "complete: %s\n", complete)
	if complete == "false" {
		fmt.Fprintf(stdout, "gaps: %s\n", res.Header.Get("X-Gaps"))
	}

	written, err := writeBody(*out, res.Body, stdout)
	if err != nil {
		return failure(stderr, err)
	}
	if *out != "-" {
		fmt.Fprintf(stdout, "wrote %d bytes to %s\n", written, *out)
	}
	return exitOK
}

// writeBody streams the response into the destination -o names: stdout for
// "-", otherwise a file, created or truncated.
func writeBody(path string, body io.Reader, stdout io.Writer) (int64, error) {
	if path == "-" {
		return io.Copy(stdout, body)
	}
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(file, body)
	if err != nil {
		file.Close()
		return written, err
	}
	return written, file.Close()
}

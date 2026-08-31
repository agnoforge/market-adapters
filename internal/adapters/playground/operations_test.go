package playground

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/adapters/httpapi"
	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/composite/adapters/acqport"
	compositeduckdb "github.com/agnos/agnoforge/internal/composite/adapters/duckdb"
	compositehttpapi "github.com/agnos/agnoforge/internal/composite/adapters/httpapi"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// The catalog is a claim about the domain routes, so the test states those
// routes itself rather than reading them back out of the thing under test:
// this list is the spec, hand-copied from httpapi.New's mux and then from the
// composite adapter's.
//
// The two declaration routes of the composite adapter — POST /composites and
// PUT /composites/{name} — are not here on purpose: their body is a nested
// document no flat Param can express, so the catalog leaves them to the CLI
// rather than offering a form that cannot be executed.
var domainRoutes = []string{
	"GET /providers",
	"POST /backfills",
	"GET /backfills/{id}",
	"DELETE /backfills/{id}",
	"GET /datasets/{provider}/{symbol}/{timeframe}/coverage",
	"GET /datasets/{provider}/{symbol}/{timeframe}/complete",
	"GET /datasets/{provider}/{symbol}/{timeframe}/gaps",
	"GET /datasets/{provider}/{symbol}/{timeframe}/bars",
	"PATCH /gaps/{id}",
	"POST /gaps/{id}/repair",
	"GET /composites",
	"GET /composites/{name}",
	"DELETE /composites/{name}",
	"POST /composites/{name}/build",
	"GET /composites/{name}/bars",
	"GET /composites/{name}/quality",
}

// One operation per domain route, in the mux's own order.
func TestCatalogCoversEveryDomainRoute(t *testing.T) {
	if len(Operations) != len(domainRoutes) {
		t.Fatalf("len(Operations) = %d, want %d", len(Operations), len(domainRoutes))
	}
	for i, op := range Operations {
		if got := op.Method + " " + op.Path; got != domainRoutes[i] {
			t.Errorf("Operations[%d] = %q, want %q", i, got, domainRoutes[i])
		}
		if op.Name == "" {
			t.Errorf("Operations[%d] (%s %s) has no name", i, op.Method, op.Path)
		}
	}
}

// Every operation names its own path wildcards, and nothing else: a form
// built from the catalog can only produce a URL the mux can route.
func TestPathWildcardsAreDeclaredAsPathParams(t *testing.T) {
	wildcard := regexp.MustCompile(`\{([^}]+)\}`)
	for _, op := range Operations {
		declared := map[string]bool{}
		for _, p := range op.Params {
			if p.In == "path" {
				declared[p.Name] = true
			}
			if p.In != "path" && p.In != "query" && p.In != "body" {
				t.Errorf("%s: param %q is in %q, want path|query|body", op.Name, p.Name, p.In)
			}
			if p.Example == "" {
				t.Errorf("%s: param %q carries no example", op.Name, p.Name)
			}
		}
		found := map[string]bool{}
		for _, m := range wildcard.FindAllStringSubmatch(op.Path, -1) {
			found[m[1]] = true
			if !declared[m[1]] {
				t.Errorf("%s: path names {%s}, which is not a path param", op.Name, m[1])
			}
		}
		for name := range declared {
			if !found[name] {
				t.Errorf("%s: path param %q is not in the path %q", op.Name, name, op.Path)
			}
		}
	}
}

// The point of the examples: substituting them into every operation and
// hitting the real API reaches a handler every time.
//
// "Reached a handler" is decided as follows. A 405 is always the mux refusing
// the method, so it fails outright. A 404 is ambiguous — the mux answers one
// for an unrouted path, and a handler answers one for a Backfill or Gap this
// empty database has never heard of — so the two are told apart by the body:
// the mux's 404 is rewritten by httpapi's own middleware to exactly
// {"error":"not found"}, while a handler names what it could not find.
func TestEveryExampleReachesAHandler(t *testing.T) {
	api := catalogAPI(t)
	for _, op := range Operations {
		t.Run(op.Method+" "+op.Path, func(t *testing.T) {
			req := requestFor(t, op)
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)
			res := rec.Result()
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)

			if res.StatusCode == http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: 405, the method is not routed", req.Method, req.URL)
			}
			if res.StatusCode == http.StatusNotFound && muxNotFound(body) {
				t.Fatalf("%s %s: 404 from the mux, the path is not routed", req.Method, req.URL)
			}
		})
	}
}

// The discriminator the test above rests on, asserted directly: an unrouted
// path is the mux's 404, a Backfill that does not exist is a handler's 404
// naming it, and a routed path at the wrong method is a 405. Without this,
// TestEveryExampleReachesAHandler would pass a catalog full of nonsense the
// day httpapi stopped answering that body.
func TestTheMuxs404IsTellableFromAHandlers404(t *testing.T) {
	api := catalogAPI(t)
	do := func(method, target string) (int, []byte) {
		t.Helper()
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
		return rec.Code, rec.Body.Bytes()
	}

	status, body := do(http.MethodGet, "/no-such-route")
	if status != http.StatusNotFound || !muxNotFound(body) {
		t.Errorf("GET /no-such-route = %d %s, want 404 {\"error\":\"not found\"}", status, body)
	}
	status, body = do(http.MethodGet, "/backfills/"+exampleBackfillID)
	if status != http.StatusNotFound || muxNotFound(body) {
		t.Errorf("GET an unknown Backfill = %d %s, want a 404 naming it", status, body)
	}
	if status, body := do(http.MethodDelete, "/providers"); status != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /providers = %d %s, want 405", status, body)
	}
}

// muxNotFound reports whether a 404 body is the mux's own, which httpapi's
// middleware writes as exactly {"error":"not found"}.
func muxNotFound(body []byte) bool {
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false
	}
	return out.Error == "not found"
}

// requestFor builds the request an operation's examples describe: path
// wildcards substituted, query params appended, body params marshalled as one
// JSON object.
func requestFor(t *testing.T, op Operation) *http.Request {
	t.Helper()
	path := op.Path
	query := url.Values{}
	body := map[string]string{}
	for _, p := range op.Params {
		switch p.In {
		case "path":
			path = strings.ReplaceAll(path, "{"+p.Name+"}", url.PathEscape(p.Example))
		case "query":
			query.Set(p.Name, p.Example)
		case "body":
			body[p.Name] = p.Example
		}
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if len(body) > 0 {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("%s: encoding the body: %v", op.Name, err)
		}
		reader = bytes.NewReader(encoded)
	}
	return httptest.NewRequest(op.Method, target, reader)
}

// catalogAPI is the real HTTP adapter over the real Store adapter and a fake
// Provider named binance, so the catalog's own provider example resolves. The
// composites resource is mounted beside it on one mux, exactly as the command
// wires the two contexts, so a composite example routes the way it really does.
func catalogAPI(t *testing.T) http.Handler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agnoforge.duckdb")
	db, err := duckdb.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	logger := slog.New(slog.DiscardHandler)
	svc := app.New(db, []app.Provider{catalogProvider{}}, app.WithLogger(logger))
	t.Cleanup(func() { svc.Shutdown(context.Background()) })

	compositeStore, err := compositeduckdb.Open(path)
	if err != nil {
		t.Fatalf("open composite store: %v", err)
	}
	t.Cleanup(func() { compositeStore.Close() })
	composites := compositehttpapi.New(
		compositeapp.New(compositeStore, acqport.New(svc), compositeapp.WithLogger(logger)), logger)

	mux := http.NewServeMux()
	mux.Handle("/composites", composites)
	mux.Handle("/composites/", composites)
	mux.Handle("/", httpapi.New(svc, logger))
	return mux
}

// catalogProvider serves nothing and reaches nothing: the routing question
// this file asks is answered before any Bar would be needed.
type catalogProvider struct{}

func (catalogProvider) Name() string { return "binance" }

func (catalogProvider) SupportedTimeframes() []domain.Timeframe {
	return []domain.Timeframe{domain.TF1m, domain.TF1h}
}

func (catalogProvider) EarliestAvailable(context.Context, domain.Symbol) (time.Time, error) {
	return origin, nil
}

func (catalogProvider) Calendar(domain.Symbol) domain.TradingCalendar { return domain.Continuous{} }

func (catalogProvider) Bars(context.Context, domain.Symbol, domain.Timeframe, domain.Range) iter.Seq2[[]domain.Bar, error] {
	return func(yield func([]domain.Bar, error) bool) {}
}

// The CLI templates spell the CLI as it actually is: positional arguments,
// the real command names, and only the flags the CLI really has.
func TestCLITemplatesMatchTheCLI(t *testing.T) {
	want := map[string]string{
		"GET /providers":         "agnoforge data providers",
		"POST /backfills":        "agnoforge data backfill {provider} {symbol} {timeframe} {start} {end}",
		"GET /backfills/{id}":    "agnoforge data status {id}",
		"DELETE /backfills/{id}": "agnoforge data cancel {id}",
		"GET /datasets/{provider}/{symbol}/{timeframe}/complete": "agnoforge data complete {provider} {symbol} {timeframe} {start} {end}",
		"GET /datasets/{provider}/{symbol}/{timeframe}/gaps":     "agnoforge data gaps {provider} {symbol} {timeframe} -status {status}",
		"GET /datasets/{provider}/{symbol}/{timeframe}/bars":     "agnoforge data query {provider} {symbol} {timeframe} {start} {end} -o bars.parquet -format {format}",
		"POST /gaps/{id}/repair":                                 "agnoforge data repair {id}",
		"GET /composites":                                        "agnoforge composite list",
		"GET /composites/{name}":                                 "agnoforge composite get {name}",
		"DELETE /composites/{name}":                              "agnoforge composite delete {name}",
		"POST /composites/{name}/build":                          "agnoforge composite build {name}",
		"GET /composites/{name}/bars":                            "agnoforge composite query {name} -o bars.parquet -timeframe {timeframe} -start {start} -end {end} -format {format}",
		"GET /composites/{name}/quality":                         "agnoforge composite quality {name}",
	}
	// The two routes the CLI has no command for.
	null := map[string]bool{
		"GET /datasets/{provider}/{symbol}/{timeframe}/coverage": true,
		"PATCH /gaps/{id}": true,
	}

	wildcard := regexp.MustCompile(`\{([^}]+)\}`)
	for _, op := range Operations {
		route := op.Method + " " + op.Path
		if null[route] {
			if op.CLI != nil {
				t.Errorf("%s: CLI = %q, want null — the CLI has no such command", route, *op.CLI)
			}
			continue
		}
		if op.CLI == nil {
			t.Errorf("%s: CLI is null, want %q", route, want[route])
			continue
		}
		if *op.CLI != want[route] {
			t.Errorf("%s: CLI = %q, want %q", route, *op.CLI, want[route])
		}
		if !strings.HasPrefix(*op.CLI, "agnoforge data ") && !strings.HasPrefix(*op.CLI, "agnoforge composite ") {
			t.Errorf("%s: CLI %q starts with neither %q nor %q",
				route, *op.CLI, "agnoforge data ", "agnoforge composite ")
		}
		if strings.Contains(*op.CLI, "--") {
			t.Errorf("%s: CLI %q uses --flag syntax, which this CLI does not have", route, *op.CLI)
		}
		named := map[string]bool{}
		for _, p := range op.Params {
			named[p.Name] = true
		}
		for _, m := range wildcard.FindAllStringSubmatch(*op.CLI, -1) {
			if !named[m[1]] {
				t.Errorf("%s: CLI names {%s}, which is not a param of the operation", route, m[1])
			}
		}
	}
}

// The catalog is served as JSON on the playground mux, beside the traces.
func TestCatalogIsServedAsJSON(t *testing.T) {
	h := newHarness(t)
	status, body := h.get("/playground/operations")
	if status != http.StatusOK {
		t.Fatalf("GET /playground/operations = %d, want 200: %s", status, body)
	}
	var served []Operation
	if err := json.Unmarshal(body, &served); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(served) != len(domainRoutes) {
		t.Fatalf("served %d operations, want %d", len(served), len(domainRoutes))
	}
	for i, op := range served {
		if got := op.Method + " " + op.Path; got != domainRoutes[i] {
			t.Errorf("served[%d] = %q, want %q", i, got, domainRoutes[i])
		}
	}
}

// The wire names are part of the contract the UI reads.
func TestCatalogJSONFieldNames(t *testing.T) {
	one, err := json.Marshal(Operation{
		Name:   "n",
		Method: "GET",
		Path:   "/p",
		Params: []Param{{Name: "id", In: "path", Required: true, Example: "1"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"name":"n","method":"GET","path":"/p","params":[{"name":"id","in":"path","required":true,"example":"1"}],"cli":null}`
	if string(one) != want {
		t.Errorf("Operation JSON =\n%s\nwant\n%s", one, want)
	}
}

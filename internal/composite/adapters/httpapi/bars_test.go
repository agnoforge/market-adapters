package httpapi_test

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/composite/domain"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// The consumer contract over the whole stack: a backtester asks a Composite
// Dataset for bars by name, timeframe and range, in JSON or as a Parquet
// stream, and never learns which provider supplied which period or whether the
// bars it got were referenced or derived.

// barJSON is one bar as the query surface answers it. It is the acquisition
// bars route's own shape, field for field, which is the point.
type barJSON struct {
	OpenTime string `json:"open_time"`
	Open     string `json:"open"`
	High     string `json:"high"`
	Low      string `json:"low"`
	Close    string `json:"close"`
	Volume   string `json:"volume"`
}

func (b barJSON) String() string {
	return fmt.Sprintf("%s o=%s h=%s l=%s c=%s v=%s",
		b.OpenTime, b.Open, b.High, b.Low, b.Close, b.Volume)
}

// barsPath spells one bars query.
func barsPath(name string, params map[string]string) string {
	query := url.Values{}
	for k, v := range params {
		query.Set(k, v)
	}
	if len(query) == 0 {
		return "/composites/" + name + "/bars"
	}
	return "/composites/" + name + "/bars?" + query.Encode()
}

// queryJSON asks for the JSON bars of one query and insists it worked.
func (h *harness) queryJSON(name string, params map[string]string) ([]barJSON, http.Header) {
	h.t.Helper()
	with := map[string]string{"format": "json"}
	for k, v := range params {
		with[k] = v
	}
	res := h.do("GET", barsPath(name, with), nil)
	var bars []barJSON
	h.decode(res, http.StatusOK, &bars)
	return bars, res.Header
}

// queryParquet asks for the same query as a Parquet stream and reads the file
// back through a DuckDB of the test's own — which is how another service
// consumes an export. The prices come back as text, so a float anywhere on the
// path would be visible here.
func (h *harness) queryParquet(name string, params map[string]string) ([]barJSON, http.Header) {
	h.t.Helper()
	res := h.do("GET", barsPath(name, params), nil)
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		h.t.Fatalf("%s: status %d, want 200 (body %s)", res.Request.URL.Path, res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/vnd.apache.parquet" {
		h.t.Fatalf("content type %q, want the parquet one", ct)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("reading the parquet stream: %v", err)
	}
	if len(body) == 0 {
		h.t.Fatal("the parquet stream was empty")
	}
	path := filepath.Join(h.t.TempDir(), "bars.parquet")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		h.t.Fatalf("writing the export: %v", err)
	}
	return readParquetBars(h.t, path), res.Header
}

// readParquetBars reads an exported file back as the same shape the JSON
// answer has, so the two encodings can be compared bar for bar.
func readParquetBars(t *testing.T, path string) []barJSON {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("second DuckDB: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT open_time, CAST("open" AS VARCHAR), CAST(high AS VARCHAR), CAST(low AS VARCHAR),
		       CAST("close" AS VARCHAR), CAST(volume AS VARCHAR)
		FROM read_parquet('` + path + `') ORDER BY open_time`)
	if err != nil {
		t.Fatalf("reading the export back: %v", err)
	}
	defer rows.Close()

	out := make([]barJSON, 0)
	for rows.Next() {
		var (
			b  barJSON
			ms int64
		)
		if err := rows.Scan(&ms, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			t.Fatalf("scanning an exported bar: %v", err)
		}
		b.OpenTime = time.UnixMilli(ms).UTC().Format(time.RFC3339)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the export back: %v", err)
	}
	return out
}

// qualityJSON is what the quality endpoint answers, reduced to what these
// tests read: what the dataset is, and the fitness numbers of its last Build.
type qualityDocument struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Mode        string `json:"mode"`
	ResolvedEnd string `json:"resolved_end"`
	Quality     *struct {
		ExpectedBars       int64   `json:"expected_bars"`
		ActualBars         int64   `json:"actual_bars"`
		CoveragePercentage float64 `json:"coverage_percentage"`
		OpenGapCount       int     `json:"open_gap_count"`
		OpenGaps           []struct {
			ID    int64  `json:"id"`
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"open_gaps"`
		IncompleteWindowCount  int    `json:"incomplete_window_count"`
		Mode                   string `json:"mode"`
		Strict                 bool   `json:"strict"`
		MaterializationVersion int    `json:"materialization_version"`
		LastBuildAt            string `json:"last_build_at"`
	} `json:"quality"`
}

// quality reads the dedicated quality endpoint.
func (h *harness) quality(name string) qualityDocument {
	h.t.Helper()
	var got qualityDocument
	h.decode(h.do("GET", "/composites/"+name+"/quality", nil), http.StatusOK, &got)
	return got
}

// --- what a query answers ---------------------------------------------------

// TestTheOneMinuteTimelineIsServedThroughTheSameEndpoint: the composite 1m
// timeline is the referenced source bars, served over the one bars route, in
// exactly the acquisition wire shape (user story 27).
func TestTheOneMinuteTimelineIsServedThroughTheSameEndpoint(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	h.build("btc-usd", http.StatusOK)

	bars, headers := h.queryJSON("btc-usd", map[string]string{"timeframe": "1m"})
	if len(bars) != 60 {
		t.Fatalf("bars = %d, want the sixty minutes of the timeline", len(bars))
	}
	want := barJSON{
		OpenTime: "2024-01-01T00:00:00Z",
		Open:     defaultBar.Open, High: defaultBar.High, Low: defaultBar.Low,
		Close: defaultBar.Close, Volume: defaultBar.Volume,
	}
	if bars[0] != want {
		t.Errorf("first bar = %s, want %s", bars[0], want)
	}
	if bars[59].OpenTime != "2024-01-01T00:59:00Z" {
		t.Errorf("last bar opens at %s, want the last minute of the hour", bars[59].OpenTime)
	}
	if headers.Get("X-Timeframe") != "1m" || headers.Get("X-Composite-State") != "ready" {
		t.Errorf("headers said %s / %s, want the 1m frame of a ready dataset",
			headers.Get("X-Timeframe"), headers.Get("X-Composite-State"))
	}
}

// TestAMaterializedFrameIsServedThroughTheSameEndpoint, from the derived bars
// rather than the source ones — and a consumer cannot tell the difference from
// the response.
func TestAMaterializedFrameIsServedThroughTheSameEndpoint(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "5m", "1h"))
	h.build("btc-usd", http.StatusOK)

	hourly, _ := h.queryJSON("btc-usd", map[string]string{"timeframe": "1h"})
	if len(hourly) != 1 {
		t.Fatalf("hourly bars = %v, want the one hour", hourly)
	}
	// Sixty flat bars: the hour opens and closes where they do, and its volume
	// is their exact sum.
	want := barJSON{
		OpenTime: "2024-01-01T00:00:00Z",
		Open:     "100.00000000", High: "101.00000000", Low: "99.00000000",
		Close: "100.50000000", Volume: "60.00000000",
	}
	if hourly[0] != want {
		t.Fatalf("hourly bar = %s, want %s", hourly[0], want)
	}
	if got, _ := h.queryJSON("btc-usd", map[string]string{"timeframe": "5m"}); len(got) != 12 {
		t.Errorf("5m bars = %d, want the twelve windows of the hour", len(got))
	}
}

// TestParquetIsTheDefaultEncodingAndAgreesWithTheJSON: the heavy-payload path
// is what an unqualified request gets, and it carries the very same bars — the
// same decimals, in the same order.
func TestParquetIsTheDefaultEncodingAndAgreesWithTheJSON(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "5m"))
	h.build("btc-usd", http.StatusOK)

	streamed, headers := h.queryParquet("btc-usd", map[string]string{"timeframe": "5m"})
	decoded, _ := h.queryJSON("btc-usd", map[string]string{"timeframe": "5m"})
	if len(streamed) != 12 {
		t.Fatalf("parquet bars = %v, want the twelve windows", streamed)
	}
	for i := range decoded {
		if streamed[i] != decoded[i] {
			t.Fatalf("bar %d: parquet %s, json %s", i, streamed[i], decoded[i])
		}
	}
	if headers.Get("X-Range-Start") != "2024-01-01T00:00:00Z" ||
		headers.Get("X-Range-End") != "2024-01-01T01:00:00Z" {
		t.Errorf("range headers = %s .. %s, want the dataset's own range",
			headers.Get("X-Range-Start"), headers.Get("X-Range-End"))
	}
}

// TestOmittingTheBoundsAsksForTheDatasetsResolvedRange: a backtester that
// names no range gets the timeline the dataset stands for, and does not have to
// know what `now` resolved to.
func TestOmittingTheBoundsAsksForTheDatasetsResolvedRange(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	h.build("btc-usd", http.StatusOK)

	whole, headers := h.queryJSON("btc-usd", nil)
	if len(whole) != 60 {
		t.Fatalf("bars = %d, want the whole resolved range", len(whole))
	}
	if headers.Get("X-Range-Start") != "2024-01-01T00:00:00Z" ||
		headers.Get("X-Range-End") != "2024-01-01T01:00:00Z" {
		t.Errorf("range = %s .. %s, want the dataset's own",
			headers.Get("X-Range-Start"), headers.Get("X-Range-End"))
	}

	// And each bound defaults on its own, half-open both ways.
	narrowed, _ := h.queryJSON("btc-usd", map[string]string{"start": "2024-01-01T00:30:00Z"})
	if len(narrowed) != 30 || narrowed[0].OpenTime != "2024-01-01T00:30:00Z" {
		t.Errorf("bars from the half hour = %d starting %s, want 30 from 00:30",
			len(narrowed), narrowed[0].OpenTime)
	}
	clipped, _ := h.queryJSON("btc-usd", map[string]string{"end": "2024-01-01T00:10:00Z"})
	if len(clipped) != 10 || clipped[9].OpenTime != "2024-01-01T00:09:00Z" {
		t.Errorf("bars to 00:10 = %d ending %s, want 10 ending 00:09", len(clipped), clipped[9].OpenTime)
	}
}

// TestTheTimelineIsServedAcrossTheSegmentsInOrder: a cross-provider dataset is
// one continuous answer, and the consumer is told nothing about the seam it
// crossed — the Segment list is where that lives.
func TestTheTimelineIsServedAcrossTheSegmentsInOrder(t *testing.T) {
	h := newHarness(t)
	half := buildStart.Add(30 * time.Minute)
	h.port.cover(acq.Range{Start: buildStart, End: half})
	h.port.coverOf(catchUpSource, acq.Range{Start: half, End: buildEnd})
	h.priceBarsOf(baseSource, "100.00000000")
	h.priceBarsOf(catchUpSource, "200.00000000")
	h.seedBarsOf(baseSource, acq.Range{Start: buildStart, End: half})
	h.seedBarsOf(catchUpSource, acq.Range{Start: half, End: buildEnd})

	body := buildable("btc-usd", "strict")
	body["catch_up"] = map[string]any{
		"kind": "source", "instrument": instrument,
		"provider": "coinbase", "symbol": catchUpSymbol, "timeframe": "1m",
	}
	h.create(body)
	built := h.build("btc-usd", http.StatusOK)
	if len(built.Segments) != 2 {
		t.Fatalf("segments = %+v, want the two providers", built.Segments)
	}

	bars, _ := h.queryJSON("btc-usd", map[string]string{"timeframe": "1m"})
	if len(bars) != 60 {
		t.Fatalf("bars = %d, want the whole hour across both providers", len(bars))
	}
	for i, b := range bars {
		want := "100.00000000"
		if i >= 30 {
			want = "200.00000000"
		}
		if b.Open != want {
			t.Fatalf("bar %d = %s, want a bar priced %s", i, b, want)
		}
	}
}

// --- the mapped failures ----------------------------------------------------

// TestQueryingAnUnknownDatasetIsANotFound.
func TestQueryingAnUnknownDatasetIsANotFound(t *testing.T) {
	h := newHarness(t)
	message := h.expectError(h.do("GET", barsPath("nothing-here", nil), nil), http.StatusNotFound)
	if !strings.Contains(message, "not found") {
		t.Errorf("message = %q, want it to say the dataset is not found", message)
	}
}

// TestQueryingADatasetThatWasNeverBuiltIsAConflict: a draft has no timeline to
// serve, and says so as the state it is in rather than answering with no bars.
func TestQueryingADatasetThatWasNeverBuiltIsAConflict(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	message := h.expectError(h.do("GET", barsPath("btc-usd", nil), nil), http.StatusConflict)
	if !strings.Contains(message, "never been built") || !strings.Contains(message, "draft") {
		t.Errorf("message = %q, want it to name the never-built draft", message)
	}
}

// TestQueryingADatasetWhoseBuildCouldNotBeReadyIsAConflict: a failed build
// leaves no Segments, so there is nothing to serve and the reason is the state.
func TestQueryingADatasetWhoseBuildCouldNotBeReadyIsAConflict(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(buildable("btc-usd", "strict"))
	h.port.openGap(7, acq.Range{Start: buildStart.Add(10 * time.Minute), End: buildStart.Add(15 * time.Minute)})
	h.build("btc-usd", http.StatusConflict)

	message := h.expectError(h.do("GET", barsPath("btc-usd", nil), nil), http.StatusConflict)
	if !strings.Contains(message, "failed") {
		t.Errorf("message = %q, want it to name the failed state", message)
	}
}

// TestQueryingATimeframeTheDatasetDoesNotMaterializeIsABadRequest, and the
// message names the frames it does serve.
func TestQueryingATimeframeTheDatasetDoesNotMaterializeIsABadRequest(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "5m", "1h"))
	h.build("btc-usd", http.StatusOK)

	message := h.expectError(
		h.do("GET", barsPath("btc-usd", map[string]string{"timeframe": "1d"}), nil),
		http.StatusBadRequest)
	if !strings.Contains(message, "1d") || !strings.Contains(message, "1m, 5m, 1h") {
		t.Errorf("message = %q, want it to refuse 1d and name 1m, 5m, 1h", message)
	}
}

// TestQueryingAnUnknownTimeframeIsABadRequest: "3m" is not a Timeframe at all,
// which is a different failure from one this dataset does not materialize.
func TestQueryingAnUnknownTimeframeIsABadRequest(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	h.build("btc-usd", http.StatusOK)
	message := h.expectError(
		h.do("GET", barsPath("btc-usd", map[string]string{"timeframe": "3m"}), nil),
		http.StatusBadRequest)
	if !strings.Contains(message, "unknown timeframe") {
		t.Errorf("message = %q, want it to refuse the unknown timeframe", message)
	}
}

// TestAMalformedQueryIsABadRequest: an unspellable bound, an empty range and
// an encoding this route does not speak.
func TestAMalformedQueryIsABadRequest(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	h.build("btc-usd", http.StatusOK)
	for _, tc := range []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"an unspellable start", map[string]string{"start": "yesterday"}, "start"},
		{"an empty range", map[string]string{
			"start": "2024-01-01T00:00:00Z", "end": "2024-01-01T00:00:00Z"}, "must be before"},
		{"an unknown format", map[string]string{"format": "csv"}, "format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := h.expectError(h.do("GET", barsPath("btc-usd", tc.params), nil), http.StatusBadRequest)
			if !strings.Contains(message, tc.want) {
				t.Errorf("message = %q, want it to mention %q", message, tc.want)
			}
		})
	}
}

// --- staleness and mode are never hidden ------------------------------------

// TestAStaleDatasetIsServedAndSaysSo: editing a built dataset makes it stale,
// queries against it still answer, and what it is comes back from get, from
// quality and from the query's own headers. Nothing pretends staleness away.
func TestAStaleDatasetIsServedAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "1h"))
	h.build("btc-usd", http.StatusOK)

	// An edit is what makes a built dataset stale (user story 21).
	edit := derivable("btc-usd", "strict", "1h")
	delete(edit, "name")
	var edited compositeJSON
	h.decode(h.do("PUT", "/composites/btc-usd", edit), http.StatusOK, &edited)
	if edited.State != "stale" {
		t.Fatalf("state after the edit = %q, want stale", edited.State)
	}

	bars, headers := h.queryJSON("btc-usd", map[string]string{"timeframe": "1h"})
	if len(bars) != 1 {
		t.Fatalf("bars = %v, want the stale dataset served anyway", bars)
	}
	if headers.Get("X-Composite-State") != "stale" {
		t.Errorf("query header said %q, want stale", headers.Get("X-Composite-State"))
	}
	if got := h.get("btc-usd"); got.State != "stale" {
		t.Errorf("get said %q, want stale", got.State)
	}
	if got := h.quality("btc-usd"); got.State != "stale" {
		t.Errorf("quality said %q, want stale", got.State)
	}
}

// TestADatasetBuiltByAnOlderMaterializationIsServedAndReadsAsStale: the derived
// staleness is discoverable the same way the edited one is.
func TestADatasetBuiltByAnOlderMaterializationIsServedAndReadsAsStale(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "1h"))
	h.build("btc-usd", http.StatusOK)
	h.setMatVersion("btc-usd", domain.MaterializationVersion-1)

	bars, headers := h.queryJSON("btc-usd", map[string]string{"timeframe": "1h"})
	if len(bars) != 1 {
		t.Fatalf("bars = %v, want the bars the older build derived, served", bars)
	}
	if headers.Get("X-Composite-State") != "stale" {
		t.Errorf("query header said %q, want stale", headers.Get("X-Composite-State"))
	}
	if got := h.quality("btc-usd"); got.State != "stale" {
		t.Errorf("quality said %q, want stale", got.State)
	}
}

// TestAResearchDatasetIsServedAndVisiblyAResearchOne: imperfect history is
// queryable, and the imperfections are listed rather than smoothed over.
func TestAResearchDatasetIsServedAndVisiblyAResearchOne(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "research", "1h"))
	gap := acq.Range{Start: buildStart.Add(12 * time.Minute), End: buildStart.Add(24 * time.Minute)}
	h.port.openGap(7, gap)
	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready — research explores imperfect history", built.State)
	}

	bars, headers := h.queryJSON("btc-usd", map[string]string{"timeframe": "1h"})
	if len(bars) != 1 {
		t.Fatalf("bars = %v, want the hour derived from what is there", bars)
	}
	if headers.Get("X-Composite-Mode") != "research" {
		t.Errorf("query header said mode %q, want research", headers.Get("X-Composite-Mode"))
	}

	q := h.quality("btc-usd")
	if q.Mode != "research" || q.Quality == nil || q.Quality.Strict {
		t.Fatalf("quality = %+v, want a research dataset that is not strict", q)
	}
	if q.Quality.OpenGapCount != 1 || len(q.Quality.OpenGaps) != 1 {
		t.Fatalf("open gaps = %+v, want the one that survived", q.Quality.OpenGaps)
	}
	if q.Quality.OpenGaps[0].ID != 7 || q.Quality.OpenGaps[0].Start != "2024-01-01T00:12:00Z" {
		t.Errorf("open gap = %+v, want acquisition's gap 7 at 00:12", q.Quality.OpenGaps[0])
	}
	if q.Quality.IncompleteWindowCount != 1 {
		t.Errorf("incomplete windows = %d, want the hour the gap falls in", q.Quality.IncompleteWindowCount)
	}
}

// --- the quality endpoint ---------------------------------------------------

// TestTheQualityEndpointAnswersThePersistedQuality: the same numbers Get
// carries, on a route of their own, so judging fitness is one request.
func TestTheQualityEndpointAnswersThePersistedQuality(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	built := h.build("btc-usd", http.StatusOK)

	got := h.quality("btc-usd")
	if got.Name != "btc-usd" || got.State != "ready" || got.Mode != "strict" {
		t.Fatalf("quality document = %+v, want the ready strict dataset", got)
	}
	if got.ResolvedEnd != "2024-01-01T01:00:00Z" {
		t.Errorf("resolved end = %q, want the built one", got.ResolvedEnd)
	}
	if got.Quality == nil {
		t.Fatal("the quality endpoint answered no quality for a built dataset")
	}
	if got.Quality.ExpectedBars != 60 || got.Quality.ActualBars != 60 ||
		got.Quality.CoveragePercentage != 100 || got.Quality.OpenGapCount != 0 {
		t.Errorf("quality = %+v, want the fully covered hour", got.Quality)
	}
	if !got.Quality.Strict || got.Quality.MaterializationVersion != domain.MaterializationVersion {
		t.Errorf("quality = %+v, want a strict dataset stamped v%d", got.Quality, domain.MaterializationVersion)
	}
	if built.Quality == nil || got.Quality.LastBuildAt != built.Quality.LastBuildAt {
		t.Errorf("last build at = %q, want the build's own %v", got.Quality.LastBuildAt, built.Quality)
	}
}

// TestTheQualityOfADatasetNoBuildHasRunIsNull, not an error: "how good is this
// dataset?" is answered by the state it is in.
func TestTheQualityOfADatasetNoBuildHasRunIsNull(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	got := h.quality("btc-usd")
	if got.State != "draft" || got.Quality != nil {
		t.Fatalf("quality document = %+v, want a draft with no quality", got)
	}
}

// TestTheQualityOfAnUnknownDatasetIsANotFound.
func TestTheQualityOfAnUnknownDatasetIsANotFound(t *testing.T) {
	h := newHarness(t)
	h.expectError(h.do("GET", "/composites/nothing-here/quality", nil), http.StatusNotFound)
}

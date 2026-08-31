package httpapi_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// Materialization over the whole stack: a Build derives the configured higher
// timeframes from the composite 1-minute timeline, records the windows the
// source data does not fully back, and stamps everything with the version it
// derived them by.

// derivable is the declaration these tests build: the same hour the other Build
// tests use, with two materialized timeframes on it.
func derivable(name, mode string, frames ...string) map[string]any {
	body := buildable(name, mode)
	body["timeframes"] = frames
	return body
}

// derivedBar is one materialized bar as it is stored: the prices as the exact
// decimal text the database holds, so a float anywhere in the derivation would
// be visible here.
type derivedBar struct {
	Timeframe                      string
	OpenTime                       time.Time
	Open, High, Low, Close, Volume string
	Version                        int
}

func (b derivedBar) String() string {
	return fmt.Sprintf("%s %s o=%s h=%s l=%s c=%s v=%s (v%d)",
		b.Timeframe, b.OpenTime.UTC().Format(time.RFC3339),
		b.Open, b.High, b.Low, b.Close, b.Volume, b.Version)
}

// derivedBars reads the bars a Build materialized for one dataset, through a
// connection of this test's own: what a Build persisted is a fact on disk, not
// something the API is asked to confirm.
func (h *harness) derivedBars(name string) []derivedBar {
	h.t.Helper()
	db, err := sql.Open("duckdb", h.path)
	if err != nil {
		h.t.Fatalf("opening the database to read the materialized bars: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT timeframe, open_time,
		       CAST("open" AS VARCHAR), CAST(high AS VARCHAR), CAST(low AS VARCHAR),
		       CAST("close" AS VARCHAR), CAST(volume AS VARCHAR), mat_version
		FROM composite_materialized_bars
		WHERE dataset = ?
		ORDER BY timeframe, open_time`, name)
	if err != nil {
		h.t.Fatalf("reading the materialized bars of %q: %v", name, err)
	}
	defer rows.Close()

	var out []derivedBar
	for rows.Next() {
		var (
			b  derivedBar
			ms int64
		)
		if err := rows.Scan(&b.Timeframe, &ms, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume, &b.Version); err != nil {
			h.t.Fatalf("scanning a materialized bar: %v", err)
		}
		b.OpenTime = time.UnixMilli(ms).UTC()
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		h.t.Fatalf("reading the materialized bars of %q: %v", name, err)
	}
	return out
}

// barsOf is the materialized bars of one timeframe, in time order.
func (h *harness) barsOf(name, timeframe string) []derivedBar {
	h.t.Helper()
	var out []derivedBar
	for _, b := range h.derivedBars(name) {
		if b.Timeframe == timeframe {
			out = append(out, b)
		}
	}
	return out
}

// setMatVersion rewrites the materialization version on a dataset row through
// the same Store the service reads, which is how a dataset built by an older
// version of this service looks.
func (h *harness) setMatVersion(name string, version int) {
	h.t.Helper()
	ctx := context.Background()
	d, err := h.store.Dataset(ctx, domain.Name(name))
	if err != nil {
		h.t.Fatalf("reading %q back: %v", name, err)
	}
	d.MatVersion = version
	if err := h.store.UpdateDataset(ctx, d); err != nil {
		h.t.Fatalf("restamping %q: %v", name, err)
	}
}

// TestABuildMaterializesTheConfiguredTimeframes is create → build → derived
// bars: the hour of seeded 1-minute bars becomes twelve 5m bars and one hourly
// one, each summarising exactly the bars underneath it.
func TestABuildMaterializesTheConfiguredTimeframes(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "5m", "1h"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}

	fiveMinute := h.barsOf("btc-usd", "5m")
	if len(fiveMinute) != 12 {
		t.Fatalf("5m bars = %v, want the twelve windows of the hour", fiveMinute)
	}
	first := fiveMinute[0]
	want := derivedBar{
		Timeframe: "5m", OpenTime: buildStart,
		Open: "100.00000000", High: "101.00000000", Low: "99.00000000",
		Close: "100.50000000", Volume: "5.00000000",
		Version: domain.MaterializationVersion,
	}
	if first.String() != want.String() {
		t.Errorf("first 5m bar = %s, want %s", first, want)
	}
	if last := fiveMinute[11]; !last.OpenTime.Equal(buildStart.Add(55 * time.Minute)) {
		t.Errorf("last 5m bar opens at %s, want 00:55", last.OpenTime)
	}

	hourly := h.barsOf("btc-usd", "1h")
	if len(hourly) != 1 || hourly[0].Volume != "60.00000000" {
		t.Fatalf("1h bars = %v, want the one hour summing all 60 minutes", hourly)
	}
	if !hourly[0].OpenTime.Equal(buildStart) {
		t.Errorf("the hourly bar opens at %s, want %s", hourly[0].OpenTime, buildStart)
	}

	// Nothing was derived for a timeframe the dataset never asked for.
	if got := h.barsOf("btc-usd", "1d"); len(got) != 0 {
		t.Errorf("1d bars = %v, want none: the dataset does not materialize that frame", got)
	}

	// And the version the bars were derived by is on the quality too.
	if built.Quality.MaterializationVersion != domain.MaterializationVersion {
		t.Errorf("quality materialization version = %d, want %d",
			built.Quality.MaterializationVersion, domain.MaterializationVersion)
	}
	if built.Quality.IncompleteWindowCount != 0 {
		t.Errorf("incomplete windows = %+v, want none over a gapless hour", built.Quality.IncompleteWindows)
	}
}

// TestBuildReportsTheIncompleteWindowsPerTimeframe: a research dataset over an
// open Gap is ready, and Quality names every window of every materialized
// timeframe the Gap falls in — flagged, not hidden, and not silently emitted.
func TestBuildReportsTheIncompleteWindowsPerTimeframe(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "research", "5m", "1h"))
	// Twelve minutes acquisition says are missing: the 5m windows at :10 and
	// :20 straddle them, the one at :15 lies wholly inside, and so does none of
	// the hour.
	h.port.openGap(7, acq.Range{Start: buildStart.Add(12 * time.Minute), End: buildStart.Add(24 * time.Minute)})

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready — research mode explores imperfect history", built.State)
	}
	if built.Quality.IncompleteWindowCount != 3 || len(built.Quality.IncompleteWindows) != 3 {
		t.Fatalf("incomplete windows = %+v, want three", built.Quality.IncompleteWindows)
	}
	want := []struct{ Timeframe, Start, End string }{
		{"5m", "2024-01-01T00:10:00Z", "2024-01-01T00:15:00Z"},
		{"5m", "2024-01-01T00:20:00Z", "2024-01-01T00:25:00Z"},
		{"1h", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z"},
	}
	for i, w := range want {
		got := built.Quality.IncompleteWindows[i]
		if got.Timeframe != w.Timeframe || got.Start != w.Start || got.End != w.End {
			t.Errorf("incomplete window %d = %+v, want %+v", i, got, w)
		}
	}

	// The flagged windows are still materialized — an incomplete window is
	// emitted and named, never dropped.
	if got := h.barsOf("btc-usd", "5m"); len(got) != 12 {
		t.Errorf("5m bars = %v, want all twelve windows, the flagged ones included", got)
	}

	// And it is persisted with the rest of the quality: Get answers the same
	// list, per timeframe, after the build is over.
	read := h.get("btc-usd").Quality
	if read == nil || read.IncompleteWindowCount != 3 {
		t.Fatalf("quality read back = %+v, want the three incomplete windows", read)
	}
	if read.IncompleteWindows[2].Timeframe != "1h" {
		t.Errorf("the third window read back = %+v, want the hourly one", read.IncompleteWindows[2])
	}
}

// TestStrictModeRefusesReadinessOverAnIncompleteWindow: the strict rule covers
// materialization too, and the failure names the windows it refused over.
func TestStrictModeRefusesReadinessOverAnIncompleteWindow(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "1h"))
	h.port.openGap(7, acq.Range{Start: buildStart.Add(12 * time.Minute), End: buildStart.Add(24 * time.Minute)})

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	if !strings.Contains(message, "materialization window") || !strings.Contains(message, "1h") {
		t.Errorf("the failure said %q, want it to name the incomplete 1h window", message)
	}

	got := h.get("btc-usd")
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Quality == nil || got.Quality.IncompleteWindowCount != 1 {
		t.Fatalf("quality = %+v, want the one incomplete window that blocked readiness", got.Quality)
	}
	// A dataset that could not be ready serves nothing derived: the bars go the
	// way the segments do.
	if bars := h.derivedBars("btc-usd"); len(bars) != 0 {
		t.Errorf("materialized bars = %v, want none — the build could not be ready", bars)
	}
}

// TestARebuildReplacesTheWindowsAndConvergesToTheSameBars is idempotence at the
// seam a researcher actually uses: building twice over the same timeline leaves
// exactly the same bars, not a second copy of them.
func TestARebuildReplacesTheWindowsAndConvergesToTheSameBars(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "5m", "1h"))

	h.build("btc-usd", http.StatusOK)
	before := h.derivedBars("btc-usd")
	h.build("btc-usd", http.StatusOK)
	after := h.derivedBars("btc-usd")

	if len(before) != 13 {
		t.Fatalf("the first build wrote %d bars, want the twelve 5m ones and the hour", len(before))
	}
	if len(after) != len(before) {
		t.Fatalf("the rebuild left %d bars where the first build left %d", len(after), len(before))
	}
	for i := range before {
		if after[i].String() != before[i].String() {
			t.Fatalf("bar %d after the rebuild = %s, want the identical %s", i, after[i], before[i])
		}
	}
}

// TestADatasetDerivedByAnotherVersionReadsAsStale: the version on the dataset
// row is what says whether the bars on disk are the ones this service would
// derive. A mismatch is stale — and a rebuild clears it.
func TestADatasetDerivedByAnotherVersionReadsAsStale(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.create(derivable("btc-usd", "strict", "1h"))

	if built := h.build("btc-usd", http.StatusOK); built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}

	// The bars were derived by an earlier version of this service.
	h.setMatVersion("btc-usd", domain.MaterializationVersion-1)
	if got := h.get("btc-usd").State; got != "stale" {
		t.Fatalf("state = %q, want stale: the bars came from another materialization version", got)
	}
	// It is visible in the list too, which is where a researcher would notice.
	var listed []compositeJSON
	h.decode(h.do("GET", "/composites", nil), http.StatusOK, &listed)
	if len(listed) != 1 || listed[0].State != "stale" {
		t.Fatalf("listed = %+v, want the one dataset, stale", listed)
	}

	// Rebuilding restamps it, and it is ready again.
	if rebuilt := h.build("btc-usd", http.StatusOK); rebuilt.State != "ready" {
		t.Fatalf("state after the rebuild = %q, want ready", rebuilt.State)
	}
	if got := h.get("btc-usd").State; got != "ready" {
		t.Fatalf("state = %q, want ready after the rebuild restamped the version", got)
	}
}

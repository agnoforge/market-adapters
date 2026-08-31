package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The contract this whole context exists for, end to end and in one test:
// declare a Composite Dataset, build it, and pull a year of a higher timeframe
// out of it — as the Parquet stream a backtester actually loads, and as the
// JSON the same range answers with — with the two agreeing decimal for decimal
// (user stories 26–28, decision 32).

// The year the backtester asks for. 2024 is a leap year, so it is 366 days —
// which is also what makes the daily windows worth asserting one by one.
var (
	yearStart = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd   = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	yearRange = acq.Range{Start: yearStart, End: yearEnd}
	yearDays  = int(yearEnd.Sub(yearStart) / (24 * time.Hour))
)

// price spells one DECIMAL(20,8) exactly, as the eight decimals the database
// stores. Every price in this test is one of these, so what a bar is worth is
// a fact the test states rather than a float it hopes for.
func price(units, ticks int) string { return fmt.Sprintf("%d.%08d", units, ticks) }

// sourceBarAt is the source bar of day d, hour h. Each of the five values names
// its own day and hour, so an aggregation that read the wrong window — or that
// passed a price through a float on the way — cannot produce the expectations
// below by accident:
//
//   - the open rises with the hour, so the first hour of the day opens the day;
//   - the high rises with the hour a hundred ticks up, so the last hour is the
//     day's high;
//   - the low rises with the hour from a lower unit, so the first hour is the
//     day's low;
//   - the close rises with the hour fifty ticks up, so the last hour closes it.
func sourceBarAt(d, h int) acq.Bar {
	return acq.Bar{
		OpenTime: yearStart.Add(time.Duration(d)*24*time.Hour + time.Duration(h)*time.Hour),
		Open:     price(1000+d, h),
		High:     price(1000+d, 100+h),
		Low:      price(900+d, h),
		Close:    price(1000+d, 50+h),
		Volume:   "0.10000000",
	}
}

// dailyBarOf is the daily bar day d must derive to: first open, highest high,
// lowest low, last close, and the exact sum of twenty-four tenths — which a
// float would sum to 2.4000000000000004.
func dailyBarOf(d int) barJSON {
	return barJSON{
		OpenTime: yearStart.Add(time.Duration(d) * 24 * time.Hour).Format(time.RFC3339),
		Open:     price(1000+d, 0),
		High:     price(1000+d, 123),
		Low:      price(900+d, 0),
		Close:    price(1000+d, 73),
		Volume:   "2.40000000",
	}
}

// seedTheYear writes the source bars of the whole year into acquisition's own
// table, one an hour.
//
// One an hour rather than one a minute is a deliberate trade: a year of real
// 1-minute bars is half a million rows, and what this test is about is the
// query surface over a year of derived bars, not how fast DuckDB inserts. The
// aggregation reads exactly the rows that are there, which is what makes the
// expectations above exact.
func (h *harness) seedTheYear() {
	h.t.Helper()
	id := acq.DatasetID{Provider: "binance", Symbol: acq.Symbol(baseSymbol), Timeframe: acq.TF1m}
	bars := make([]acq.Bar, 0, yearDays*24)
	for d := 0; d < yearDays; d++ {
		for hour := 0; hour < 24; hour++ {
			bars = append(bars, sourceBarAt(d, hour))
		}
	}
	if err := h.source.UpsertBars(context.Background(), id, bars); err != nil {
		h.t.Fatalf("seeding the year: %v", err)
	}
}

// TestABacktesterPullsAYearOfDailyBarsAsParquetAndAsJSON is the backtester
// contract: create → build → query, twice over the same range in the two
// encodings, and the answers are the same bars down to the last decimal.
func TestABacktesterPullsAYearOfDailyBarsAsParquetAndAsJSON(t *testing.T) {
	h := newHarness(t)
	h.port.cover(yearRange)
	h.seedTheYear()
	h.create(map[string]any{
		"name":       "btc-usd",
		"instrument": instrument,
		"base": map[string]any{
			"instrument": instrument, "provider": "binance",
			"symbol": baseSymbol, "timeframe": "1m",
		},
		"requested_start": "2024-01-01T00:00:00Z",
		"requested_end":   "2025-01-01T00:00:00Z",
		"timeframes":      []string{"1d", "1M"},
		"mode":            "strict",
	})

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if built.ResolvedEnd != "2025-01-01T00:00:00Z" {
		t.Fatalf("resolved end = %q, want the end of the year", built.ResolvedEnd)
	}

	// What every one of the 366 daily bars must be, stated ahead of the query.
	want := make([]barJSON, 0, yearDays)
	for d := 0; d < yearDays; d++ {
		want = append(want, dailyBarOf(d))
	}

	// The heavy-payload path first, because it is the one a backtester takes:
	// no format, so Parquet, over the dataset's own resolved range.
	streamed, headers := h.queryParquet("btc-usd", map[string]string{"timeframe": "1d"})
	assertBarsEqual(t, streamed, want, "the parquet stream")
	if headers.Get("X-Range-Start") != "2024-01-01T00:00:00Z" ||
		headers.Get("X-Range-End") != "2025-01-01T00:00:00Z" {
		t.Errorf("range headers = %s .. %s, want the whole year",
			headers.Get("X-Range-Start"), headers.Get("X-Range-End"))
	}
	if headers.Get("X-Composite-State") != "ready" || headers.Get("X-Composite-Mode") != "strict" {
		t.Errorf("headers said %s / %s, want a ready strict dataset",
			headers.Get("X-Composite-State"), headers.Get("X-Composite-Mode"))
	}

	// The same range as JSON: the same bars, the same decimals, in the same
	// order. A float anywhere on either path would separate the two here.
	decoded, _ := h.queryJSON("btc-usd", map[string]string{
		"timeframe": "1d", "start": "2024-01-01T00:00:00Z", "end": "2025-01-01T00:00:00Z",
	})
	assertBarsEqual(t, decoded, want, "the json answer")
	assertBarsEqual(t, decoded, streamed, "the parquet stream")

	// The second materialized frame is the calendar one, and it is served by the
	// same route: twelve monthly bars, opening on the 1st at 00:00 UTC.
	monthly, _ := h.queryJSON("btc-usd", map[string]string{"timeframe": "1M"})
	if len(monthly) != 12 {
		t.Fatalf("monthly bars = %d, want the twelve months of the year", len(monthly))
	}
	if monthly[0].OpenTime != "2024-01-01T00:00:00Z" || monthly[11].OpenTime != "2024-12-01T00:00:00Z" {
		t.Errorf("monthly bars run %s .. %s, want January .. December",
			monthly[0].OpenTime, monthly[11].OpenTime)
	}
	// February 2024 has 29 days: the leap day is in the bar, and its volume says
	// so exactly.
	if monthly[1].Volume != "69.60000000" {
		t.Errorf("february volume = %s, want the 29 leap-year days at 2.4 each", monthly[1].Volume)
	}

	// A range inside the year is honoured half-open, on the derived windows.
	quarter, _ := h.queryJSON("btc-usd", map[string]string{
		"timeframe": "1d", "start": "2024-03-01T00:00:00Z", "end": "2024-04-01T00:00:00Z",
	})
	if len(quarter) != 31 {
		t.Fatalf("march bars = %d, want the 31 days of March", len(quarter))
	}
	if quarter[0] != dailyBarOf(60) || quarter[30] != dailyBarOf(90) {
		t.Errorf("march runs %s .. %s, want the 61st .. 91st day of a leap year",
			quarter[0], quarter[30])
	}

	// And the fitness of what was just queried is one request away, with the
	// materialization version the bars were derived by.
	q := h.quality("btc-usd")
	if q.State != "ready" || q.Mode != "strict" || q.Quality == nil {
		t.Fatalf("quality document = %+v, want the ready strict dataset", q)
	}
	if q.Quality.MaterializationVersion != domain.MaterializationVersion {
		t.Errorf("materialization version = %d, want %d",
			q.Quality.MaterializationVersion, domain.MaterializationVersion)
	}
	if q.Quality.OpenGapCount != 0 || q.Quality.IncompleteWindowCount != 0 {
		t.Errorf("quality = %+v, want no gaps and no incomplete windows", q.Quality)
	}
}

// assertBarsEqual insists on exactly these bars, in this order, decimal for
// decimal.
func assertBarsEqual(t *testing.T, got, want []barJSON, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s answered %d bars, want %d", what, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: bar %d = %s, want %s", what, i, got[i], want[i])
		}
	}
}

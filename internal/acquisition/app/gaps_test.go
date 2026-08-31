package app_test

import (
	"context"
	"iter"
	"slices"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/acquisition/app"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// weekendCalendar is a market that closes over the weekend: a continuous
// calendar with every Saturday and Sunday open_time removed. It is the
// non-continuous TradingCalendar the use cases must already cope with, even
// though the only Provider built so far is continuous.
type weekendCalendar struct{}

func (weekendCalendar) Expected(r domain.Range, tf domain.Timeframe) iter.Seq[time.Time] {
	return func(yield func(time.Time) bool) {
		for t := range (domain.Continuous{}).Expected(r, tf) {
			if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
				continue
			}
			if !yield(t) {
				return
			}
		}
	}
}

// seedCoverage records r as requested for the Dataset, without running a
// Backfill: gap detection is judged on the state of a Dataset, not on the way
// it got there.
func seedCoverage(t *testing.T, store *duckdb.Store, ds domain.DatasetID, r domain.Range) {
	t.Helper()
	if err := store.ExtendCoverage(context.Background(), ds, r); err != nil {
		t.Fatalf("ExtendCoverage %s: %v", r, err)
	}
}

// seedBars writes bars into the Dataset.
func seedBars(t *testing.T, store *duckdb.Store, ds domain.DatasetID, bars []domain.Bar) {
	t.Helper()
	if err := store.UpsertBars(context.Background(), ds, bars); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
}

// barsAt builds one valid Bar per open_time.
func barsAt(times ...time.Time) []domain.Bar {
	out := make([]domain.Bar, 0, len(times))
	for _, ot := range times {
		out = append(out, domain.Bar{
			OpenTime: ot.UTC(),
			Open:     "10",
			High:     "12",
			Low:      "9",
			Close:    "11",
			Volume:   "100",
		})
	}
	return out
}

// except returns the n offsets from 0 with the given ones left out — the way
// a test states which Bars a Dataset is holding.
func except(n int, absent ...int) []int {
	out := make([]int, 0, n)
	for i := range n {
		if !slices.Contains(absent, i) {
			out = append(out, i)
		}
	}
	return out
}

// detect runs gap detection over r and fails the test if it errors.
func detect(t *testing.T, svc *app.Service, ds domain.DatasetID, r domain.Range) []domain.Gap {
	t.Helper()
	gaps, err := svc.DetectGaps(context.Background(), ds, r)
	if err != nil {
		t.Fatalf("DetectGaps %s: %v", r, err)
	}
	return gaps
}

// gapStatus reads one Gap back by id.
func gapStatus(t *testing.T, store *duckdb.Store, gapID int64) domain.Gap {
	t.Helper()
	g, err := store.Gap(context.Background(), gapID)
	if err != nil {
		t.Fatalf("Gap %d: %v", gapID, err)
	}
	return g
}

// setStatus moves a Gap the way an operator would.
func setStatus(t *testing.T, store *duckdb.Store, gapID int64, s domain.GapStatus, reason string) {
	t.Helper()
	if err := store.SetGapStatus(context.Background(), gapID, s, reason); err != nil {
		t.Fatalf("SetGapStatus %d %s: %v", gapID, s, err)
	}
}

func TestDetectGapsRecordsOneOpenGapPerContiguousRun(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	svc := newService(store, provider("fake"))

	seedCoverage(t, store, ds, rng(0, 60))
	seedBars(t, store, ds, testBarsAt(origin, tf, except(60, 10, 11, 12, 13, 14, 30, 31)...))

	got := detect(t, svc, ds, rng(0, 60))
	assertGaps(t, got, rng(10, 15), rng(30, 32))
	assertGaps(t, gapsOf(t, store, ds), rng(10, 15), rng(30, 32))
	for i, g := range got {
		if g.ID == 0 {
			t.Errorf("gap %d has no id", i)
		}
		if g.Dataset != ds {
			t.Errorf("gap %d dataset = %s, want %s", i, g.Dataset, ds)
		}
	}
}

func TestDetectGapsNeverReportsMinutesOutsideCoverage(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	svc := newService(store, provider("fake"))

	// Only the first half hour was ever asked for; the Dataset holds its
	// first five minutes.
	seedCoverage(t, store, ds, rng(0, 30))
	seedBars(t, store, ds, testBars(origin, tf, 5))

	got := detect(t, svc, ds, rng(0, 60))
	assertGaps(t, got, rng(5, 30))
	assertGaps(t, gapsOf(t, store, ds), rng(5, 30))
}

func TestDetectGapsOverAWeekendClosingCalendar(t *testing.T) {
	// A Friday, so the range spans one closed weekend.
	friday := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	tuesday := friday.AddDate(0, 0, 4)
	whole := domain.Range{Start: friday, End: tuesday}
	hourly := domain.DatasetID{Provider: "fake", Symbol: "BTCUSDT", Timeframe: domain.TF1h}

	p := provider("fake")
	p.calendar = weekendCalendar{}

	// expected is every open_time the calendar asks for over the range.
	var expected []time.Time
	for ot := range (weekendCalendar{}).Expected(whole, domain.TF1h) {
		expected = append(expected, ot)
	}
	if len(expected) != 48 {
		t.Fatalf("%d expected open times, want the 48 hours of Friday and Monday", len(expected))
	}

	t.Run("no gap over the closed period", func(t *testing.T) {
		store := newStore(t)
		svc := newService(store, p)
		seedCoverage(t, store, hourly, whole)
		// Every open_time the calendar expects is held; the weekend is not.
		seedBars(t, store, hourly, barsAt(expected...))

		if got := detect(t, svc, hourly, whole); len(got) != 0 {
			t.Fatalf("gaps = %v, want none: a closed market is not a Gap", got)
		}
	})

	t.Run("a run spanning the closed period is one gap", func(t *testing.T) {
		store := newStore(t)
		svc := newService(store, p)
		seedCoverage(t, store, hourly, whole)
		// The last hour of Friday and the first hour of Monday are absent:
		// consecutive in the calendar's expected sequence, so one run.
		friLast := friday.Add(23 * time.Hour)
		monFirst := friday.AddDate(0, 0, 3)
		var held []time.Time
		for _, ot := range expected {
			if !ot.Equal(friLast) && !ot.Equal(monFirst) {
				held = append(held, ot)
			}
		}
		seedBars(t, store, hourly, barsAt(held...))

		want := domain.Range{Start: friLast, End: monFirst.Add(time.Hour)}
		assertGaps(t, detect(t, svc, hourly, whole), want)
		assertGaps(t, gapsOf(t, store, hourly), want)
	})
}

func TestDetectGapsExcludesSettledRanges(t *testing.T) {
	store := newStore(t)
	ds := dataset("fake")
	svc := newService(store, provider("fake"))

	seedCoverage(t, store, ds, rng(0, 60))
	seedBars(t, store, ds, testBarsAt(origin, tf, except(60, 10, 11, 12, 13, 14, 30, 31)...))

	found := detect(t, svc, ds, rng(0, 60))
	if len(found) != 2 {
		t.Fatalf("%d gaps, want 2", len(found))
	}
	setStatus(t, store, found[0].ID, domain.GapIgnored, "provider outage")
	setStatus(t, store, found[1].ID, domain.GapUnrecoverable, "never served")

	// Bars are no longer expected there, so nothing is re-reported and the
	// operator's decisions survive.
	if got := detect(t, svc, ds, rng(0, 60)); len(got) != 0 {
		t.Fatalf("gaps = %v, want none: settled ranges are not expected", got)
	}
	after := gapsOf(t, store, ds)
	if len(after) != 2 {
		t.Fatalf("%d gaps stored, want the 2 settled ones: %v", len(after), after)
	}
	want := []struct {
		r      domain.Range
		status domain.GapStatus
		reason string
	}{
		{rng(10, 15), domain.GapIgnored, "provider outage"},
		{rng(30, 32), domain.GapUnrecoverable, "never served"},
	}
	for i, g := range after {
		if g.Range != want[i].r || g.Status != want[i].status || g.Reason != want[i].reason {
			t.Errorf("gap %d = %s %s %q, want %s %s %q",
				i, g.Range, g.Status, g.Reason, want[i].r, want[i].status, want[i].reason)
		}
	}
}

func TestDetectGapsMarksAFilledGapRepaired(t *testing.T) {
	cases := []struct {
		name   string
		settle domain.GapStatus
	}{
		{"open gap", ""},
		{"ignored gap", domain.GapIgnored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			ds := dataset("fake")
			svc := newService(store, provider("fake"))

			seedCoverage(t, store, ds, rng(0, 60))
			seedBars(t, store, ds, testBarsAt(origin, tf, except(60, 10, 11, 12, 13, 14)...))

			found := detect(t, svc, ds, rng(0, 60))
			if len(found) != 1 {
				t.Fatalf("%d gaps, want 1: %v", len(found), found)
			}
			gapID := found[0].ID
			if tc.settle != "" {
				setStatus(t, store, gapID, tc.settle, "operator said so")
			}

			// A Repair lands the Bars the Gap was about.
			seedBars(t, store, ds, testBarsAt(origin, tf, 10, 11, 12, 13, 14))

			if got := detect(t, svc, ds, rng(0, 60)); len(got) != 0 {
				t.Fatalf("gaps = %v, want none", got)
			}
			g := gapStatus(t, store, gapID)
			if g.Status != domain.GapRepaired {
				t.Errorf("gap %d status = %q, want %q", gapID, g.Status, domain.GapRepaired)
			}
			if g.Range != rng(10, 15) {
				t.Errorf("gap %d range = %s, want %s", gapID, g.Range, rng(10, 15))
			}
			if all := gapsOf(t, store, ds); len(all) != 1 {
				t.Errorf("%d gaps stored, want the one repaired record: %v", len(all), all)
			}
		})
	}
}

func TestDetectGapsCoalescesByTimeframe(t *testing.T) {
	store := newStore(t)
	five := domain.TF5m
	ds := domain.DatasetID{Provider: "fake", Symbol: "BTCUSDT", Timeframe: five}
	svc := newService(store, provider("fake"))

	// Twelve five-minute Bars cover the hour; the 4th, 5th and 8th are absent.
	whole := domain.Range{Start: origin, End: origin.Add(time.Hour)}
	seedCoverage(t, store, ds, whole)
	seedBars(t, store, ds, testBarsAt(origin, five, except(12, 3, 4, 7)...))

	bar := func(i int) time.Time { return origin.Add(time.Duration(i) * five.Duration()) }
	first := domain.Range{Start: bar(3), End: bar(5)}
	second := domain.Range{Start: bar(7), End: bar(8)}
	assertGaps(t, detect(t, svc, ds, whole), first, second)
	assertGaps(t, gapsOf(t, store, ds), first, second)
}

func TestIsComplete(t *testing.T) {
	cases := []struct {
		name      string
		coverage  domain.Range
		gap       domain.Range
		gapStatus domain.GapStatus
		ask       domain.Range
		want      bool
		wantGaps  []domain.Range
	}{
		{
			name:     "range not covered",
			coverage: rng(0, 30),
			ask:      rng(0, 60),
			want:     false,
		},
		{
			name:      "covered with an open gap inside",
			coverage:  rng(0, 60),
			gap:       rng(10, 15),
			gapStatus: domain.GapOpen,
			ask:       rng(0, 20),
			want:      false,
			wantGaps:  []domain.Range{rng(10, 15)},
		},
		{
			name:      "covered with an open gap only touching",
			coverage:  rng(0, 60),
			gap:       rng(10, 15),
			gapStatus: domain.GapOpen,
			ask:       rng(15, 30),
			want:      true,
		},
		{
			name:      "covered with an ignored gap inside",
			coverage:  rng(0, 60),
			gap:       rng(10, 15),
			gapStatus: domain.GapIgnored,
			ask:       rng(0, 20),
			want:      true,
		},
		{
			name:      "covered with an unrecoverable gap inside",
			coverage:  rng(0, 60),
			gap:       rng(10, 15),
			gapStatus: domain.GapUnrecoverable,
			ask:       rng(0, 20),
			want:      true,
		},
		{
			name:      "covered with a repaired gap inside",
			coverage:  rng(0, 60),
			gap:       rng(10, 15),
			gapStatus: domain.GapRepaired,
			ask:       rng(0, 20),
			want:      true,
		},
		{
			name:     "covered and clean",
			coverage: rng(0, 60),
			ask:      rng(0, 60),
			want:     true,
		},
		{
			name:     "empty range",
			coverage: rng(0, 30),
			ask:      rng(10, 10),
			want:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			ds := dataset("fake")
			svc := newService(store, provider("fake"))
			seedCoverage(t, store, ds, tc.coverage)
			if !tc.gap.IsEmpty() {
				g := domain.Gap{Dataset: ds, Range: tc.gap, Status: domain.GapOpen}
				if err := store.ReplaceOpenGaps(context.Background(), ds, tc.gap, []domain.Gap{g}); err != nil {
					t.Fatalf("ReplaceOpenGaps: %v", err)
				}
				stored := gapsOf(t, store, ds)
				if len(stored) != 1 {
					t.Fatalf("%d gaps seeded, want 1", len(stored))
				}
				if tc.gapStatus != domain.GapOpen {
					setStatus(t, store, stored[0].ID, tc.gapStatus, "seeded")
				}
			}

			got, err := svc.IsComplete(context.Background(), ds, tc.ask)
			if err != nil {
				t.Fatalf("IsComplete: %v", err)
			}
			if got.Complete != tc.want {
				t.Errorf("complete = %v, want %v", got.Complete, tc.want)
			}
			if len(got.Gaps) != len(tc.wantGaps) {
				t.Fatalf("%d gaps, want %d: %v", len(got.Gaps), len(tc.wantGaps), got.Gaps)
			}
			for i, g := range got.Gaps {
				if g.Range != tc.wantGaps[i] {
					t.Errorf("gap %d range = %s, want %s", i, g.Range, tc.wantGaps[i])
				}
				if g.Status != domain.GapOpen {
					t.Errorf("gap %d status = %q, want %q", i, g.Status, domain.GapOpen)
				}
			}
		})
	}
}

// An open Gap straddling the detected range is re-detected in full, not
// truncated to the part inside the range.
func TestDetectGapsRedetectsAStraddlingGapInFull(t *testing.T) {
	store := newStore(t)
	ds := domain.DatasetID{Provider: "fake", Symbol: "BTCUSDT", Timeframe: tf}
	svc := newService(store, provider("fake"))
	ctx := context.Background()

	// Coverage [0,100); bars everywhere except [40,60).
	var times []time.Time
	for i := 0; i < 100; i++ {
		if i < 40 || i >= 60 {
			times = append(times, at(i))
		}
	}
	seedBars(t, store, ds, barsAt(times...))
	if err := store.ExtendCoverage(ctx, ds, rng(0, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DetectGaps(ctx, ds, rng(0, 100)); err != nil {
		t.Fatal(err)
	}

	// Re-detect over [50,150) only: the Gap [40,60) intersects, so it is
	// deleted and must come back whole.
	if _, err := svc.DetectGaps(ctx, ds, rng(50, 150)); err != nil {
		t.Fatal(err)
	}
	gaps, err := store.Gaps(ctx, ds, app.GapFilter{})
	if err != nil || len(gaps) != 1 || gaps[0].Range != rng(40, 60) {
		t.Fatalf("gaps = %v (%v), want one gap %s", gaps, err, rng(40, 60))
	}
	c, err := svc.IsComplete(ctx, ds, rng(40, 50))
	if err != nil || c.Complete {
		t.Fatalf("IsComplete([40,50)) = %v (%v), want false", c.Complete, err)
	}
}

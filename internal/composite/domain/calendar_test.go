package domain_test

import (
	"slices"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// ut is a UTC instant, spelled the way a boundary table reads.
func ut(y int, m time.Month, d, h, min, sec, ms int) time.Time {
	return time.Date(y, m, d, h, min, sec, ms*int(time.Millisecond), time.UTC)
}

// TestFixedWindowStartsAreEpochAnchored is checkbox 2 in table form: the
// fixed frames align on multiples of their own duration measured from the
// Unix epoch, including before it, where the arithmetic must floor rather
// than truncate toward zero.
func TestFixedWindowStartsAreEpochAnchored(t *testing.T) {
	for _, tc := range []struct {
		name string
		tf   domain.Timeframe
		at   time.Time
		want time.Time
	}{
		{"1m mid-minute", domain.TF1m, ut(2024, time.March, 1, 12, 34, 56, 789), ut(2024, time.March, 1, 12, 34, 0, 0)},
		{"1m on boundary", domain.TF1m, ut(2024, time.March, 1, 12, 34, 0, 0), ut(2024, time.March, 1, 12, 34, 0, 0)},
		{"5m", domain.TF5m, ut(2024, time.March, 1, 12, 34, 56, 789), ut(2024, time.March, 1, 12, 30, 0, 0)},
		{"15m", domain.TF15m, ut(2024, time.March, 1, 12, 34, 56, 789), ut(2024, time.March, 1, 12, 30, 0, 0)},
		{"30m", domain.TF30m, ut(2024, time.March, 1, 12, 34, 56, 789), ut(2024, time.March, 1, 12, 30, 0, 0)},
		{"30m first half", domain.TF30m, ut(2024, time.March, 1, 12, 29, 59, 999), ut(2024, time.March, 1, 12, 0, 0, 0)},
		{"1h", domain.TF1h, ut(2024, time.March, 1, 12, 34, 56, 789), ut(2024, time.March, 1, 12, 0, 0, 0)},
		{"4h", domain.TF4h, ut(2024, time.March, 1, 12, 34, 56, 789), ut(2024, time.March, 1, 12, 0, 0, 0)},
		{"4h just before a boundary", domain.TF4h, ut(2024, time.March, 1, 11, 59, 59, 999), ut(2024, time.March, 1, 8, 0, 0, 0)},
		{"1d", domain.TF1d, ut(2024, time.March, 1, 12, 34, 56, 789), ut(2024, time.March, 1, 0, 0, 0, 0)},
		{"1d at the epoch", domain.TF1d, ut(1970, time.January, 1, 0, 0, 0, 0), ut(1970, time.January, 1, 0, 0, 0, 0)},
		{"1d before the epoch", domain.TF1d, ut(1969, time.December, 31, 23, 59, 59, 999), ut(1969, time.December, 31, 0, 0, 0, 0)},
		{"4h before the epoch", domain.TF4h, ut(1969, time.December, 31, 23, 59, 59, 999), ut(1969, time.December, 31, 20, 0, 0, 0)},
		{"1h before the epoch", domain.TF1h, ut(1969, time.December, 31, 23, 59, 59, 999), ut(1969, time.December, 31, 23, 0, 0, 0)},
		{"5m before the epoch", domain.TF5m, ut(1969, time.December, 31, 23, 59, 59, 999), ut(1969, time.December, 31, 23, 55, 0, 0)},
		{"1m one ms before the epoch", domain.TF1m, ut(1969, time.December, 31, 23, 59, 59, 999), ut(1969, time.December, 31, 23, 59, 0, 0)},
		{"1d across a year boundary", domain.TF1d, ut(2025, time.January, 1, 0, 0, 0, 1), ut(2025, time.January, 1, 0, 0, 0, 0)},
		{"1d on Feb 29", domain.TF1d, ut(2024, time.February, 29, 23, 59, 59, 999), ut(2024, time.February, 29, 0, 0, 0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tf.WindowStart(tc.at)
			if !got.Equal(tc.want) {
				t.Fatalf("%q.WindowStart(%s) = %s, want %s", tc.tf, tc.at.Format(time.RFC3339Nano), got.Format(time.RFC3339Nano), tc.want.Format(time.RFC3339Nano))
			}
			if got.Location() != time.UTC {
				t.Errorf("WindowStart returned %s, want a UTC instant", got.Location())
			}
			// A fixed window is exactly one duration long and half-open around at.
			w := tc.tf.Window(tc.at)
			if !w.Start.Equal(tc.want) || !w.End.Equal(tc.want.Add(tc.tf.Duration())) {
				t.Errorf("Window(%s) = %s, want [%s,%s)", tc.at, w, tc.want, tc.want.Add(tc.tf.Duration()))
			}
			if !w.Contains(tc.at) {
				t.Errorf("Window(%s) = %s does not contain its own instant", tc.at, w)
			}
		})
	}
}

// TestFixedWindowStartsMatchAcquisition is the other half of checkbox 2: the
// two contexts are compared directly, so they cannot drift apart. Acquisition
// enumerates the boundaries it expects; this context must produce exactly the
// same instants for the same range.
func TestFixedWindowStartsMatchAcquisition(t *testing.T) {
	// A range whose bounds sit on every fixed boundary, spanning a leap day
	// and a month boundary.
	r := acq.Range{Start: ut(2024, time.February, 28, 0, 0, 0, 0), End: ut(2024, time.March, 2, 0, 0, 0, 0)}
	for _, tf := range domain.Timeframes() {
		if !tf.Fixed() {
			continue
		}
		t.Run(tf.String(), func(t *testing.T) {
			atf, ok := tf.Acquisition()
			if !ok {
				t.Fatalf("%q does not convert to an acquisition timeframe", tf)
			}
			want := slices.Collect(acq.Continuous{}.Expected(r, atf))
			got := slices.Collect(tf.WindowStarts(r))
			if len(want) == 0 {
				t.Fatal("acquisition expected no boundaries; the fixture is wrong")
			}
			if len(got) != len(want) {
				t.Fatalf("%q yielded %d window starts, acquisition expected %d", tf, len(got), len(want))
			}
			for i := range want {
				if !got[i].Equal(want[i]) {
					t.Fatalf("%q window start %d = %s, acquisition says %s", tf, i, got[i].Format(time.RFC3339), want[i].Format(time.RFC3339))
				}
			}
			// And per-instant: every acquisition boundary is its own window start.
			for _, b := range want {
				if s := tf.WindowStart(b); !s.Equal(b) {
					t.Errorf("%q.WindowStart(%s) = %s, want the boundary itself", tf, b, s)
				}
				if s := tf.WindowStart(b.Add(tf.Duration() - time.Millisecond)); !s.Equal(b) {
					t.Errorf("%q: instant just before the next boundary belongs to %s, want %s", tf, s, b)
				}
			}
		})
	}
}

// TestWeekWindowStartsAreMondayUTC is checkbox 3 for `1w`: ISO-8601 weeks
// start Monday 00:00 UTC, including where the week straddles a year boundary,
// a leap day, and the Unix epoch (a Thursday, so the week that contains it
// starts before the epoch).
func TestWeekWindowStartsAreMondayUTC(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{"Monday 00:00 is its own start", ut(2024, time.December, 30, 0, 0, 0, 0), ut(2024, time.December, 30, 0, 0, 0, 0)},
		{"Monday 23:59 same week", ut(2024, time.December, 30, 23, 59, 59, 999), ut(2024, time.December, 30, 0, 0, 0, 0)},
		{"New Year's Day mid-week (Wed 2025)", ut(2025, time.January, 1, 12, 34, 56, 0), ut(2024, time.December, 30, 0, 0, 0, 0)},
		{"Sunday closing the year-straddling week", ut(2025, time.January, 5, 23, 59, 59, 999), ut(2024, time.December, 30, 0, 0, 0, 0)},
		{"next Monday opens a new week", ut(2025, time.January, 6, 0, 0, 0, 0), ut(2025, time.January, 6, 0, 0, 0, 0)},
		{"New Year's Day on a Friday (2021)", ut(2021, time.January, 1, 0, 0, 0, 0), ut(2020, time.December, 28, 0, 0, 0, 0)},
		{"New Year's Eve 2020 (Thursday)", ut(2020, time.December, 31, 23, 0, 0, 0), ut(2020, time.December, 28, 0, 0, 0, 0)},
		{"New Year's Day on a Sunday (2023)", ut(2023, time.January, 1, 0, 0, 0, 0), ut(2022, time.December, 26, 0, 0, 0, 0)},
		{"New Year's Day on a Monday (2024)", ut(2024, time.January, 1, 0, 0, 0, 0), ut(2024, time.January, 1, 0, 0, 0, 0)},
		{"leap day 2024 (Thursday)", ut(2024, time.February, 29, 12, 0, 0, 0), ut(2024, time.February, 26, 0, 0, 0, 0)},
		{"day after the leap day", ut(2024, time.March, 1, 0, 0, 0, 0), ut(2024, time.February, 26, 0, 0, 0, 0)},
		{"leap day 2000 (Tuesday)", ut(2000, time.February, 29, 6, 0, 0, 0), ut(2000, time.February, 28, 0, 0, 0, 0)},
		{"the epoch is a Thursday", ut(1970, time.January, 1, 0, 0, 0, 0), ut(1969, time.December, 29, 0, 0, 0, 0)},
		{"just before the epoch", ut(1969, time.December, 31, 23, 59, 59, 999), ut(1969, time.December, 29, 0, 0, 0, 0)},
		{"the first full week after the epoch", ut(1970, time.January, 5, 0, 0, 0, 0), ut(1970, time.January, 5, 0, 0, 0, 0)},
		{"a non-leap February end", ut(2023, time.February, 28, 23, 59, 59, 999), ut(2023, time.February, 27, 0, 0, 0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := domain.TF1w.WindowStart(tc.at)
			if !got.Equal(tc.want) {
				t.Fatalf("1w.WindowStart(%s) = %s, want %s", tc.at.Format(time.RFC3339), got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
			if got.Weekday() != time.Monday {
				t.Errorf("week starts on a %s, want Monday", got.Weekday())
			}
			if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 || got.Nanosecond() != 0 {
				t.Errorf("week starts at %02d:%02d:%02d, want 00:00:00 UTC", h, m, s)
			}
			w := domain.TF1w.Window(tc.at)
			if d := w.End.Sub(w.Start); d != 7*24*time.Hour {
				t.Errorf("week %s lasts %v, want 168h", w, d)
			}
			if !w.Contains(tc.at) {
				t.Errorf("week %s does not contain %s", w, tc.at)
			}
		})
	}
}

// TestMonthWindowStartsAreTheFirstUTC is checkbox 3 for `1M`: months start on
// the 1st at 00:00 UTC and last as long as the calendar says — 28, 29, 30 or
// 31 days, across year boundaries and both leap-year rules.
func TestMonthWindowStartsAreTheFirstUTC(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		want time.Time
		days int
	}{
		{"31-day January", ut(2024, time.January, 31, 23, 59, 59, 999), ut(2024, time.January, 1, 0, 0, 0, 0), 31},
		{"the 1st is its own start", ut(2024, time.January, 1, 0, 0, 0, 0), ut(2024, time.January, 1, 0, 0, 0, 0), 31},
		{"29-day February in a leap year", ut(2024, time.February, 29, 12, 0, 0, 0), ut(2024, time.February, 1, 0, 0, 0, 0), 29},
		{"28-day February in a common year", ut(2023, time.February, 28, 23, 59, 59, 999), ut(2023, time.February, 1, 0, 0, 0, 0), 28},
		{"29-day February in 2000 (divisible by 400)", ut(2000, time.February, 29, 0, 0, 0, 0), ut(2000, time.February, 1, 0, 0, 0, 0), 29},
		{"28-day February in 1900 (divisible by 100)", ut(1900, time.February, 28, 12, 0, 0, 0), ut(1900, time.February, 1, 0, 0, 0, 0), 28},
		{"28-day February in 2100 (divisible by 100)", ut(2100, time.February, 28, 12, 0, 0, 0), ut(2100, time.February, 1, 0, 0, 0, 0), 28},
		{"30-day April", ut(2024, time.April, 30, 23, 59, 59, 999), ut(2024, time.April, 1, 0, 0, 0, 0), 30},
		{"30-day November", ut(2024, time.November, 15, 8, 0, 0, 0), ut(2024, time.November, 1, 0, 0, 0, 0), 30},
		{"December rolls into the next year", ut(2024, time.December, 31, 23, 59, 59, 999), ut(2024, time.December, 1, 0, 0, 0, 0), 31},
		{"January of the next year", ut(2025, time.January, 1, 0, 0, 0, 0), ut(2025, time.January, 1, 0, 0, 0, 0), 31},
		{"the epoch month", ut(1970, time.January, 1, 0, 0, 0, 0), ut(1970, time.January, 1, 0, 0, 0, 0), 31},
		{"before the epoch", ut(1969, time.December, 15, 6, 30, 0, 0), ut(1969, time.December, 1, 0, 0, 0, 0), 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := domain.TF1M.WindowStart(tc.at)
			if !got.Equal(tc.want) {
				t.Fatalf("1M.WindowStart(%s) = %s, want %s", tc.at.Format(time.RFC3339), got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
			if got.Day() != 1 {
				t.Errorf("month starts on day %d, want the 1st", got.Day())
			}
			if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 || got.Nanosecond() != 0 {
				t.Errorf("month starts at %02d:%02d:%02d, want 00:00:00 UTC", h, m, s)
			}
			w := domain.TF1M.Window(tc.at)
			if d := w.End.Sub(w.Start); d != time.Duration(tc.days)*24*time.Hour {
				t.Errorf("month %s lasts %v, want %d days", w, d, tc.days)
			}
			if w.End.Day() != 1 {
				t.Errorf("month %s ends on day %d, want the 1st of the next month", w, w.End.Day())
			}
			if !w.Contains(tc.at) {
				t.Errorf("month %s does not contain %s", w, tc.at)
			}
		})
	}
}

// TestCalendarWindowsTileTheTimelineWithoutHoleOrOverlap walks several years of
// consecutive weeks and months and asserts each window's end is exactly the
// next window's start — the property every gap-free composite timeline needs.
func TestCalendarWindowsTileTheTimelineWithoutHoleOrOverlap(t *testing.T) {
	for _, tf := range []domain.Timeframe{domain.TF1w, domain.TF1M, domain.TF1d, domain.TF4h} {
		t.Run(tf.String(), func(t *testing.T) {
			r := acq.Range{Start: ut(1969, time.November, 3, 0, 0, 0, 0), End: ut(1971, time.February, 1, 0, 0, 0, 0)}
			var prev acq.Range
			n := 0
			for s := range tf.WindowStarts(r) {
				w := tf.Window(s)
				if !w.Start.Equal(s) {
					t.Fatalf("Window(%s).Start = %s", s, w.Start)
				}
				if w.IsEmpty() {
					t.Fatalf("window at %s is empty", s)
				}
				if n > 0 {
					if !prev.End.Equal(w.Start) {
						t.Fatalf("window %s does not abut the previous %s", w, prev)
					}
					if prev.Overlaps(w) {
						t.Fatalf("window %s overlaps the previous %s", w, prev)
					}
				}
				prev = w
				n++
			}
			if n == 0 {
				t.Fatal("no windows over a 15-month range")
			}
		})
	}
}

// TestWindowStartsCoverTheRange is checkbox 4: iteration yields every window
// that overlaps the half-open range — including the one the range starts in
// the middle of, so no source minute is ever silently dropped — and stops
// before any window that starts at or after the range end.
func TestWindowStartsCoverTheRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		tf   domain.Timeframe
		r    acq.Range
		want []time.Time
	}{
		{
			name: "1h from mid-window",
			tf:   domain.TF1h,
			r:    acq.Range{Start: ut(2024, time.January, 1, 0, 30, 0, 0), End: ut(2024, time.January, 1, 2, 0, 0, 0)},
			want: []time.Time{ut(2024, time.January, 1, 0, 0, 0, 0), ut(2024, time.January, 1, 1, 0, 0, 0)},
		},
		{
			name: "1h ending one ms into a window",
			tf:   domain.TF1h,
			r:    acq.Range{Start: ut(2024, time.January, 1, 0, 30, 0, 0), End: ut(2024, time.January, 1, 2, 0, 0, 1)},
			want: []time.Time{ut(2024, time.January, 1, 0, 0, 0, 0), ut(2024, time.January, 1, 1, 0, 0, 0), ut(2024, time.January, 1, 2, 0, 0, 0)},
		},
		{
			name: "1w across a year boundary",
			tf:   domain.TF1w,
			r:    acq.Range{Start: ut(2024, time.December, 31, 0, 0, 0, 0), End: ut(2025, time.January, 14, 0, 0, 0, 0)},
			want: []time.Time{ut(2024, time.December, 30, 0, 0, 0, 0), ut(2025, time.January, 6, 0, 0, 0, 0), ut(2025, time.January, 13, 0, 0, 0, 0)},
		},
		{
			name: "1M across a year boundary",
			tf:   domain.TF1M,
			r:    acq.Range{Start: ut(2024, time.November, 15, 0, 0, 0, 0), End: ut(2025, time.February, 1, 0, 0, 0, 0)},
			want: []time.Time{ut(2024, time.November, 1, 0, 0, 0, 0), ut(2024, time.December, 1, 0, 0, 0, 0), ut(2025, time.January, 1, 0, 0, 0, 0)},
		},
		{
			name: "1M over a single leap February",
			tf:   domain.TF1M,
			r:    acq.Range{Start: ut(2024, time.February, 29, 23, 59, 59, 999), End: ut(2024, time.March, 1, 0, 0, 0, 0)},
			want: []time.Time{ut(2024, time.February, 1, 0, 0, 0, 0)},
		},
		{
			name: "1d over a leap day",
			tf:   domain.TF1d,
			r:    acq.Range{Start: ut(2024, time.February, 28, 0, 0, 0, 0), End: ut(2024, time.March, 1, 0, 0, 0, 0)},
			want: []time.Time{ut(2024, time.February, 28, 0, 0, 0, 0), ut(2024, time.February, 29, 0, 0, 0, 0)},
		},
		{
			name: "empty range yields nothing",
			tf:   domain.TF1h,
			r:    acq.Range{Start: ut(2024, time.January, 1, 0, 0, 0, 0), End: ut(2024, time.January, 1, 0, 0, 0, 0)},
			want: nil,
		},
		{
			name: "inverted range yields nothing",
			tf:   domain.TF1h,
			r:    acq.Range{Start: ut(2024, time.January, 2, 0, 0, 0, 0), End: ut(2024, time.January, 1, 0, 0, 0, 0)},
			want: nil,
		},
		{
			name: "unknown timeframe yields nothing",
			tf:   domain.Timeframe("1y"),
			r:    acq.Range{Start: ut(2024, time.January, 1, 0, 0, 0, 0), End: ut(2025, time.January, 1, 0, 0, 0, 0)},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := slices.Collect(tc.tf.WindowStarts(tc.r))
			if len(got) != len(tc.want) {
				t.Fatalf("%q.WindowStarts(%s) yielded %d starts %v, want %d %v", tc.tf, tc.r, len(got), got, len(tc.want), tc.want)
			}
			for i := range tc.want {
				if !got[i].Equal(tc.want[i]) {
					t.Errorf("start %d = %s, want %s", i, got[i].Format(time.RFC3339), tc.want[i].Format(time.RFC3339))
				}
			}
			// Every yielded window overlaps the range, and together they cover it.
			for _, s := range got {
				if w := tc.tf.Window(s); !w.Overlaps(tc.r) {
					t.Errorf("window %s does not overlap %s", w, tc.r)
				}
			}
			if len(got) > 0 {
				if first := tc.tf.Window(got[0]); first.Start.After(tc.r.Start) {
					t.Errorf("first window %s starts after the range %s, leaving its head uncovered", first, tc.r)
				}
				if last := tc.tf.Window(got[len(got)-1]); last.End.Before(tc.r.End) {
					t.Errorf("last window %s ends before the range %s, leaving its tail uncovered", last, tc.r)
				}
			}
		})
	}
}

// TestWindowStartsStopsWhenTheCallerBreaks keeps the iterator honest: a
// consumer that stops early must not run the sequence to its end.
func TestWindowStartsStopsWhenTheCallerBreaks(t *testing.T) {
	r := acq.Range{Start: ut(2020, time.January, 1, 0, 0, 0, 0), End: ut(2025, time.January, 1, 0, 0, 0, 0)}
	n := 0
	for range domain.TF1M.WindowStarts(r) {
		n++
		if n == 3 {
			break
		}
	}
	if n != 3 {
		t.Fatalf("iterated %d windows after breaking at 3", n)
	}
}

// TestWindowStartsNormalisesToUTC: a range given in another zone yields the
// same UTC window starts, because this context is UTC-only.
func TestWindowStartsNormalisesToUTC(t *testing.T) {
	zone := time.FixedZone("UTC+9", 9*60*60)
	r := acq.Range{
		Start: ut(2024, time.December, 31, 0, 0, 0, 0).In(zone),
		End:   ut(2025, time.January, 14, 0, 0, 0, 0).In(zone),
	}
	got := slices.Collect(domain.TF1w.WindowStarts(r))
	want := []time.Time{ut(2024, time.December, 30, 0, 0, 0, 0), ut(2025, time.January, 6, 0, 0, 0, 0), ut(2025, time.January, 13, 0, 0, 0, 0)}
	if len(got) != len(want) {
		t.Fatalf("yielded %v, want %v", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) || got[i].Location() != time.UTC {
			t.Errorf("start %d = %s (%s), want %s UTC", i, got[i], got[i].Location(), want[i])
		}
	}
	if s := domain.TF1M.WindowStart(ut(2025, time.January, 1, 2, 0, 0, 0).In(zone)); !s.Equal(ut(2025, time.January, 1, 0, 0, 0, 0)) {
		t.Errorf("1M.WindowStart of a non-UTC instant = %s, want 2025-01-01T00:00:00Z", s)
	}
}

// TestWindowOfAnUnknownTimeframeIsEmpty: an unknown frame has no arithmetic,
// and says so rather than inventing a boundary.
func TestWindowOfAnUnknownTimeframeIsEmpty(t *testing.T) {
	tf := domain.Timeframe("1y")
	if s := tf.WindowStart(ut(2024, time.January, 1, 0, 0, 0, 0)); !s.IsZero() {
		t.Errorf("WindowStart of an unknown timeframe = %s, want the zero time", s)
	}
	if w := tf.Window(ut(2024, time.January, 1, 0, 0, 0, 0)); !w.IsEmpty() {
		t.Errorf("Window of an unknown timeframe = %s, want the empty range", w)
	}
}

package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

var _ domain.TradingCalendar = domain.Continuous{}

func collect(c domain.TradingCalendar, r domain.Range, tf domain.Timeframe) []time.Time {
	return slices.Collect(c.Expected(r, tf))
}

func TestContinuousYieldsExactlyNPerNMinutes(t *testing.T) {
	for _, n := range []int{1, 2, 60, 1440, 44640} {
		start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		r := domain.Range{Start: start, End: start.Add(time.Duration(n) * time.Minute)}
		got := collect(domain.Continuous{}, r, "1m")
		if len(got) != n {
			t.Fatalf("%d-minute range at 1m yielded %d open times, want %d", n, len(got), n)
		}
		if !got[0].Equal(start) {
			t.Errorf("first open time = %v, want %v", got[0], start)
		}
		want := start.Add(time.Duration(n-1) * time.Minute)
		if !got[len(got)-1].Equal(want) {
			t.Errorf("last open time = %v, want %v", got[len(got)-1], want)
		}
	}
}

func TestContinuousBinanceMonthOf1mBars(t *testing.T) {
	r := domain.Range{
		Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	if got := len(collect(domain.Continuous{}, r, "1m")); got != 44640 {
		t.Errorf("January 2024 at 1m = %d bars, want 44640", got)
	}
}

func TestContinuousAlignsStartUpToBoundary(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		tf    domain.Timeframe
		want  []time.Time
	}{
		{
			name:  "aligned start is included",
			start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 1, 1, 0, 3, 0, 0, time.UTC),
			tf:    "1m",
			want: []time.Time{
				time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC),
				time.Date(2024, 1, 1, 0, 2, 0, 0, time.UTC),
			},
		},
		{
			name:  "unaligned start rounds up",
			start: time.Date(2024, 1, 1, 0, 0, 30, 0, time.UTC),
			end:   time.Date(2024, 1, 1, 0, 3, 0, 0, time.UTC),
			tf:    "1m",
			want: []time.Time{
				time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC),
				time.Date(2024, 1, 1, 0, 2, 0, 0, time.UTC),
			},
		},
		{
			name:  "5m boundaries from epoch",
			start: time.Date(2024, 1, 1, 0, 2, 0, 0, time.UTC),
			end:   time.Date(2024, 1, 1, 0, 16, 0, 0, time.UTC),
			tf:    "5m",
			want: []time.Time{
				time.Date(2024, 1, 1, 0, 5, 0, 0, time.UTC),
				time.Date(2024, 1, 1, 0, 10, 0, 0, time.UTC),
				time.Date(2024, 1, 1, 0, 15, 0, 0, time.UTC),
			},
		},
		{
			name:  "4h boundaries from epoch",
			start: time.Date(2024, 3, 5, 1, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 3, 5, 13, 0, 0, 0, time.UTC),
			tf:    "4h",
			want: []time.Time{
				time.Date(2024, 3, 5, 4, 0, 0, 0, time.UTC),
				time.Date(2024, 3, 5, 8, 0, 0, 0, time.UTC),
				time.Date(2024, 3, 5, 12, 0, 0, 0, time.UTC),
			},
		},
		{
			name:  "1d boundaries are UTC midnights",
			start: time.Date(2024, 3, 5, 6, 0, 0, 0, time.UTC),
			end:   time.Date(2024, 3, 8, 0, 0, 0, 0, time.UTC),
			tf:    "1d",
			want: []time.Time{
				time.Date(2024, 3, 6, 0, 0, 0, 0, time.UTC),
				time.Date(2024, 3, 7, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			// 3d bars are multiples of 3 days from the Unix epoch (1970-01-01).
			name:  "3d boundaries from the unix epoch",
			start: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			end:   time.Date(1970, 1, 10, 0, 0, 0, 0, time.UTC),
			tf:    "3d",
			want: []time.Time{
				time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(1970, 1, 4, 0, 0, 0, 0, time.UTC),
				time.Date(1970, 1, 7, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name:  "before the epoch aligns up too",
			start: time.Date(1969, 12, 31, 23, 30, 0, 0, time.UTC),
			end:   time.Date(1970, 1, 1, 1, 0, 0, 0, time.UTC),
			tf:    "1h",
			want: []time.Time{
				time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collect(domain.Continuous{}, domain.Range{Start: tt.start, End: tt.end}, tt.tf)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v (%d), want %v (%d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range tt.want {
				if !got[i].Equal(tt.want[i]) {
					t.Errorf("[%d] = %v, want %v", i, got[i], tt.want[i])
				}
				if got[i].Location() != time.UTC {
					t.Errorf("[%d] location = %v, want UTC", i, got[i].Location())
				}
			}
		})
	}
}

func TestContinuousEndIsExclusive(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	r := domain.Range{Start: start, End: start.Add(3 * time.Minute)}
	got := collect(domain.Continuous{}, r, "1m")
	for _, tt := range got {
		if !tt.Before(r.End) {
			t.Errorf("yielded %v which is not before exclusive end %v", tt, r.End)
		}
	}
}

func TestContinuousEmptyCases(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		r    domain.Range
		tf   domain.Timeframe
	}{
		{"empty range", domain.Range{Start: start, End: start}, "1m"},
		{"reversed range", domain.Range{Start: start.Add(time.Hour), End: start}, "1m"},
		{"zero range", domain.Range{}, "1m"},
		{"unsupported timeframe", domain.Range{Start: start, End: start.Add(time.Hour)}, "1w"},
		{"range shorter than one bar", domain.Range{Start: start.Add(time.Second), End: start.Add(30 * time.Second)}, "1m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collect(domain.Continuous{}, tt.r, tt.tf); len(got) != 0 {
				t.Errorf("got %v, want no open times", got)
			}
		})
	}
}

func TestContinuousExpectedStopsEarly(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	r := domain.Range{Start: start, End: start.Add(1000 * time.Minute)}
	n := 0
	for range (domain.Continuous{}).Expected(r, "1m") {
		n++
		if n == 3 {
			break
		}
	}
	if n != 3 {
		t.Errorf("break after 3 yielded %d iterations", n)
	}
}

func TestContinuousInputNotUTC(t *testing.T) {
	loc := time.FixedZone("UTC+7", 7*3600)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	r := domain.Range{Start: start.In(loc), End: start.Add(3 * time.Minute).In(loc)}
	got := collect(domain.Continuous{}, r, "1m")
	if len(got) != 3 {
		t.Fatalf("got %d open times, want 3", len(got))
	}
	if got[0].Location() != time.UTC || !got[0].Equal(start) {
		t.Errorf("first = %v (%v), want %v UTC", got[0], got[0].Location(), start)
	}
}

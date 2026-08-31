package domain_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// tm builds a UTC instant h hours after 2024-01-01T00:00:00Z.
func tm(h int) time.Time {
	return time.Date(2024, 1, 1, h, 0, 0, 0, time.UTC)
}

func rg(a, b int) domain.Range { return domain.Range{Start: tm(a), End: tm(b)} }

func TestRangeIsEmpty(t *testing.T) {
	tests := []struct {
		name string
		r    domain.Range
		want bool
	}{
		{"zero value", domain.Range{}, true},
		{"start equals end", rg(3, 3), true},
		{"end before start", rg(5, 2), true},
		{"normal", rg(1, 2), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRangeContainsIsHalfOpen(t *testing.T) {
	r := rg(2, 5)
	tests := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"before start", tm(1), false},
		{"at start (inclusive)", tm(2), true},
		{"inside", tm(3), true},
		{"at end (exclusive)", tm(5), false},
		{"after end", tm(6), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Contains(tt.t); got != tt.want {
				t.Errorf("Contains(%v) = %v, want %v", tt.t, got, tt.want)
			}
		})
	}
	if rg(3, 3).Contains(tm(3)) {
		t.Error("empty range must contain nothing")
	}
}

func TestRangeOverlapsAndTouches(t *testing.T) {
	tests := []struct {
		name              string
		a, b              domain.Range
		overlaps, touches bool
	}{
		{"disjoint with hole", rg(0, 2), rg(4, 6), false, false},
		{"touching a then b", rg(0, 2), rg(2, 4), false, true},
		{"touching b then a", rg(2, 4), rg(0, 2), false, true},
		{"partial overlap", rg(0, 3), rg(2, 5), true, false},
		{"contained", rg(0, 10), rg(3, 4), true, false},
		{"identical", rg(1, 5), rg(1, 5), true, false},
		{"shared start", rg(1, 5), rg(1, 3), true, false},
		{"shared end", rg(1, 5), rg(3, 5), true, false},
		{"empty vs normal", rg(3, 3), rg(0, 6), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Overlaps(tt.b); got != tt.overlaps {
				t.Errorf("a.Overlaps(b) = %v, want %v", got, tt.overlaps)
			}
			if got := tt.b.Overlaps(tt.a); got != tt.overlaps {
				t.Errorf("b.Overlaps(a) = %v, want %v (must be symmetric)", got, tt.overlaps)
			}
			if got := tt.a.Touches(tt.b); got != tt.touches {
				t.Errorf("a.Touches(b) = %v, want %v", got, tt.touches)
			}
			if got := tt.b.Touches(tt.a); got != tt.touches {
				t.Errorf("b.Touches(a) = %v, want %v (must be symmetric)", got, tt.touches)
			}
		})
	}
}

func TestRangeUnion(t *testing.T) {
	tests := []struct {
		name   string
		a, b   domain.Range
		want   domain.Range
		wantOK bool
	}{
		{"disjoint does not merge", rg(0, 2), rg(4, 6), domain.Range{}, false},
		{"touching merges", rg(0, 2), rg(2, 4), rg(0, 4), true},
		{"touching merges reversed", rg(2, 4), rg(0, 2), rg(0, 4), true},
		{"overlapping merges", rg(0, 3), rg(2, 5), rg(0, 5), true},
		{"contained merges to outer", rg(0, 10), rg(3, 4), rg(0, 10), true},
		{"identical", rg(1, 5), rg(1, 5), rg(1, 5), true},
		{"empty absorbs into other", rg(3, 3), rg(1, 5), rg(1, 5), true},
		{"other empty", rg(1, 5), rg(3, 3), rg(1, 5), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.a.Union(tt.b)
			if ok != tt.wantOK {
				t.Fatalf("Union ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Union = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRangeIntersect(t *testing.T) {
	tests := []struct {
		name string
		a, b domain.Range
		want domain.Range
	}{
		{"disjoint", rg(0, 2), rg(4, 6), domain.Range{}},
		{"touching yields empty", rg(0, 2), rg(2, 4), domain.Range{}},
		{"partial", rg(0, 3), rg(2, 5), rg(2, 3)},
		{"contained", rg(0, 10), rg(3, 4), rg(3, 4)},
		{"identical", rg(1, 5), rg(1, 5), rg(1, 5)},
		{"empty operand", rg(3, 3), rg(0, 6), domain.Range{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Intersect(tt.b)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("a.Intersect(b) = %v, want %v", got, tt.want)
			}
			if got := tt.b.Intersect(tt.a); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("b.Intersect(a) = %v, want %v (must be symmetric)", got, tt.want)
			}
		})
	}
}

func TestRangeSubtract(t *testing.T) {
	tests := []struct {
		name string
		a, b domain.Range
		want []domain.Range
	}{
		{"disjoint keeps whole", rg(0, 2), rg(4, 6), []domain.Range{rg(0, 2)}},
		{"touching keeps whole", rg(0, 2), rg(2, 4), []domain.Range{rg(0, 2)}},
		{"identical removes all", rg(1, 5), rg(1, 5), nil},
		{"superset removes all", rg(3, 4), rg(0, 10), nil},
		{"middle punches hole", rg(0, 10), rg(4, 6), []domain.Range{rg(0, 4), rg(6, 10)}},
		{"trims left", rg(0, 10), rg(0, 4), []domain.Range{rg(4, 10)}},
		{"trims right", rg(0, 10), rg(6, 10), []domain.Range{rg(0, 6)}},
		{"overlap from left", rg(2, 10), rg(0, 4), []domain.Range{rg(4, 10)}},
		{"overlap from right", rg(0, 8), rg(6, 12), []domain.Range{rg(0, 6)}},
		{"empty base", rg(3, 3), rg(0, 10), nil},
		{"empty subtrahend keeps whole", rg(0, 10), rg(3, 3), []domain.Range{rg(0, 10)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Subtract(tt.b)
			if len(got) != len(tt.want) {
				t.Fatalf("Subtract = %v (%d pieces), want %v (%d pieces)", got, len(got), tt.want, len(tt.want))
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Subtract = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeRanges(t *testing.T) {
	tests := []struct {
		name string
		in   []domain.Range
		want []domain.Range
	}{
		{"nil", nil, nil},
		{"single", []domain.Range{rg(1, 3)}, []domain.Range{rg(1, 3)}},
		{"drops empties", []domain.Range{rg(2, 2), rg(1, 3), rg(5, 4)}, []domain.Range{rg(1, 3)}},
		{"sorts", []domain.Range{rg(6, 8), rg(1, 3)}, []domain.Range{rg(1, 3), rg(6, 8)}},
		{"coalesces touching", []domain.Range{rg(2, 4), rg(0, 2), rg(4, 6)}, []domain.Range{rg(0, 6)}},
		{"coalesces overlapping", []domain.Range{rg(0, 5), rg(3, 9)}, []domain.Range{rg(0, 9)}},
		{"coalesces contained", []domain.Range{rg(0, 20), rg(3, 4), rg(5, 6)}, []domain.Range{rg(0, 20)}},
		{"keeps disjoint", []domain.Range{rg(0, 2), rg(4, 6), rg(8, 10)}, []domain.Range{rg(0, 2), rg(4, 6), rg(8, 10)}},
		{"identical duplicates", []domain.Range{rg(1, 5), rg(1, 5)}, []domain.Range{rg(1, 5)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := append([]domain.Range(nil), tt.in...)
			got := domain.MergeRanges(in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeRanges = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(in, tt.in) {
				t.Errorf("MergeRanges mutated its input: %v, was %v", in, tt.in)
			}
		})
	}
}

func TestSubtractRanges(t *testing.T) {
	tests := []struct {
		name        string
		base, minus []domain.Range
		want        []domain.Range
	}{
		{"nothing to subtract", []domain.Range{rg(0, 10)}, nil, []domain.Range{rg(0, 10)}},
		{"empty base", nil, []domain.Range{rg(0, 10)}, nil},
		{"punches one hole", []domain.Range{rg(0, 10)}, []domain.Range{rg(4, 6)}, []domain.Range{rg(0, 4), rg(6, 10)}},
		{"punches two holes", []domain.Range{rg(0, 10)}, []domain.Range{rg(2, 3), rg(6, 7)}, []domain.Range{rg(0, 2), rg(3, 6), rg(7, 10)}},
		{"removes everything", []domain.Range{rg(0, 4), rg(6, 10)}, []domain.Range{rg(0, 12)}, nil},
		{"disjoint minus", []domain.Range{rg(0, 4)}, []domain.Range{rg(6, 10)}, []domain.Range{rg(0, 4)}},
		{"touching minus", []domain.Range{rg(0, 4)}, []domain.Range{rg(4, 10)}, []domain.Range{rg(0, 4)}},
		{"merges base first", []domain.Range{rg(0, 5), rg(5, 10)}, []domain.Range{rg(4, 6)}, []domain.Range{rg(0, 4), rg(6, 10)}},
		{"overlapping minus entries", []domain.Range{rg(0, 10)}, []domain.Range{rg(2, 5), rg(4, 8)}, []domain.Range{rg(0, 2), rg(8, 10)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.SubtractRanges(tt.base, tt.minus)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SubtractRanges = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRangeOpsNormaliseToUTC(t *testing.T) {
	loc := time.FixedZone("UTC+7", 7*3600)
	a := domain.Range{Start: tm(0).In(loc), End: tm(6).In(loc)}
	b := domain.Range{Start: tm(4).In(loc), End: tm(10).In(loc)}

	u, ok := a.Union(b)
	if !ok {
		t.Fatal("Union of overlapping ranges failed")
	}
	if !reflect.DeepEqual(u, rg(0, 10)) {
		t.Errorf("Union = %v, want %v (UTC-normalised)", u, rg(0, 10))
	}
	if got := a.Intersect(b); !reflect.DeepEqual(got, rg(4, 6)) {
		t.Errorf("Intersect = %v, want %v (UTC-normalised)", got, rg(4, 6))
	}
	if got := a.Subtract(b); !reflect.DeepEqual(got, []domain.Range{rg(0, 4)}) {
		t.Errorf("Subtract = %v, want %v (UTC-normalised)", got, []domain.Range{rg(0, 4)})
	}
}

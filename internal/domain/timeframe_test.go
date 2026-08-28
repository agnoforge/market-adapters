package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

func TestParseTimeframe(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"1m", time.Minute},
		{"3m", 3 * time.Minute},
		{"5m", 5 * time.Minute},
		{"15m", 15 * time.Minute},
		{"30m", 30 * time.Minute},
		{"1h", time.Hour},
		{"2h", 2 * time.Hour},
		{"4h", 4 * time.Hour},
		{"6h", 6 * time.Hour},
		{"8h", 8 * time.Hour},
		{"12h", 12 * time.Hour},
		{"1d", 24 * time.Hour},
		{"3d", 72 * time.Hour},
	}
	if len(tests) != 13 {
		t.Fatalf("expected 13 supported timeframes in table, got %d", len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			tf, err := domain.ParseTimeframe(tt.in)
			if err != nil {
				t.Fatalf("ParseTimeframe(%q) error: %v", tt.in, err)
			}
			if got := tf.String(); got != tt.in {
				t.Errorf("String() = %q, want %q", got, tt.in)
			}
			if got := tf.Duration(); got != tt.want {
				t.Errorf("Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseTimeframeRejects(t *testing.T) {
	for _, in := range []string{"1s", "1w", "1M", "", "60m", "1M ", " 1m", "1H", "1D", "7d", "1y", "m1", "0m"} {
		t.Run(in, func(t *testing.T) {
			tf, err := domain.ParseTimeframe(in)
			if err == nil {
				t.Fatalf("ParseTimeframe(%q) = %q, want error", in, tf)
			}
			if !errors.Is(err, domain.ErrUnsupportedTimeframe) {
				t.Errorf("error %v does not wrap ErrUnsupportedTimeframe", err)
			}
		})
	}
}

func TestTimeframesOrderedAndComplete(t *testing.T) {
	want := []domain.Timeframe{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d"}
	got := domain.Timeframes()
	if len(got) != len(want) {
		t.Fatalf("Timeframes() has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Timeframes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Ordered by ascending duration.
	for i := 1; i < len(got); i++ {
		if got[i].Duration() <= got[i-1].Duration() {
			t.Errorf("Timeframes() not ascending at %d: %v then %v", i, got[i-1], got[i])
		}
	}
	// Caller mutation must not corrupt the package list.
	got[0] = "bogus"
	if domain.Timeframes()[0] != "1m" {
		t.Error("Timeframes() returns a shared slice; caller mutated it")
	}
}

func TestTimeframeDurationOfUnknownIsZero(t *testing.T) {
	if d := domain.Timeframe("1w").Duration(); d != 0 {
		t.Errorf("Duration() of unsupported timeframe = %v, want 0", d)
	}
}

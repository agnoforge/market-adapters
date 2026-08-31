package domain_test

import (
	"errors"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// TestCanonicalTimeframesRoundTrip pins the canonical set: every short form
// parses to itself and formats back to itself, in ascending order.
func TestCanonicalTimeframesRoundTrip(t *testing.T) {
	want := []domain.Timeframe{"1m", "5m", "15m", "30m", "1h", "4h", "1d", "1w", "1M"}
	got := domain.Timeframes()
	if len(got) != len(want) {
		t.Fatalf("Timeframes() has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Timeframes()[%d] = %q, want %q", i, got[i], want[i])
		}
		tf, err := domain.ParseTimeframe(string(want[i]))
		if err != nil {
			t.Fatalf("ParseTimeframe(%q) = %v, want a timeframe", want[i], err)
		}
		if tf.String() != string(want[i]) {
			t.Errorf("ParseTimeframe(%q).String() = %q", want[i], tf)
		}
		if !tf.Valid() {
			t.Errorf("%q is not Valid()", tf)
		}
		if tf.Order() != i {
			t.Errorf("%q.Order() = %d, want %d", tf, tf.Order(), i)
		}
	}
}

// TestParseTimeframeRejectsEverythingElse covers the near-misses a caller is
// most likely to type, including acquisition frames this context does not
// serve and case variations of the two calendar frames.
func TestParseTimeframeRejectsEverythingElse(t *testing.T) {
	for _, s := range []string{
		"", " ", "1m ", " 1m", "1s", "2m", "3m", "2h", "6h", "8h", "12h", "3d", "7d",
		"1W", "1mo", "1MO", "1y", "1Y", "60m", "m1", "0m", "1D", "1H", "week", "month",
	} {
		t.Run(s, func(t *testing.T) {
			tf, err := domain.ParseTimeframe(s)
			if err == nil {
				t.Fatalf("ParseTimeframe(%q) = %q, want an error", s, tf)
			}
			if !errors.Is(err, domain.ErrInvalidConfig) {
				t.Errorf("error %v does not wrap ErrInvalidConfig", err)
			}
			if domain.Timeframe(s).Valid() {
				t.Errorf("%q reports Valid()", s)
			}
		})
	}
}

// TestFixedAndCalendarFrames splits the canonical set the way the boundary
// arithmetic does: seven fixed-length frames and the two calendar frames,
// which have no constant duration at all.
func TestFixedAndCalendarFrames(t *testing.T) {
	fixed := map[domain.Timeframe]time.Duration{
		domain.TF1m:  time.Minute,
		domain.TF5m:  5 * time.Minute,
		domain.TF15m: 15 * time.Minute,
		domain.TF30m: 30 * time.Minute,
		domain.TF1h:  time.Hour,
		domain.TF4h:  4 * time.Hour,
		domain.TF1d:  24 * time.Hour,
	}
	for tf, want := range fixed {
		if !tf.Fixed() {
			t.Errorf("%q.Fixed() = false, want true", tf)
		}
		if got := tf.Duration(); got != want {
			t.Errorf("%q.Duration() = %v, want %v", tf, got, want)
		}
	}
	for _, tf := range []domain.Timeframe{domain.TF1w, domain.TF1M} {
		if tf.Fixed() {
			t.Errorf("%q.Fixed() = true, want false: a calendar frame has no constant length", tf)
		}
		if got := tf.Duration(); got != 0 {
			t.Errorf("%q.Duration() = %v, want 0", tf, got)
		}
	}
	if got := domain.Timeframe("nonsense").Duration(); got != 0 {
		t.Errorf("unknown timeframe Duration() = %v, want 0", got)
	}
	if domain.Timeframe("nonsense").Fixed() {
		t.Error("unknown timeframe reports Fixed()")
	}
}

// TestAcquisitionConversion is checkbox 5: every fixed frame converts to a
// timeframe acquisition itself accepts, and the two calendar frames cannot be
// converted at all — the caller gets the zero acquisition Timeframe, which
// acquisition rejects, and an explicit false.
func TestAcquisitionConversion(t *testing.T) {
	for _, tf := range domain.Timeframes() {
		got, ok := tf.Acquisition()
		if tf == domain.TF1w || tf == domain.TF1M {
			if ok {
				t.Errorf("%q.Acquisition() = %q, true; a calendar frame must not convert", tf, got)
			}
			if got != "" {
				t.Errorf("%q.Acquisition() returned %q, want the zero acquisition Timeframe", tf, got)
			}
			if got.Valid() {
				t.Errorf("%q.Acquisition() returned a timeframe acquisition considers valid", tf)
			}
			if _, err := acq.ParseTimeframe(string(got)); err == nil {
				t.Errorf("acquisition parsed %q, so a calendar frame leaked across the boundary", got)
			}
			continue
		}
		if !ok {
			t.Errorf("%q.Acquisition() = false, want a conversion", tf)
			continue
		}
		if got.String() != tf.String() {
			t.Errorf("%q.Acquisition() = %q, want the same short form", tf, got)
		}
		if !got.Valid() {
			t.Errorf("%q.Acquisition() = %q, which acquisition does not support", tf, got)
		}
		if got.Duration() != tf.Duration() {
			t.Errorf("%q lasts %v here and %v in acquisition", tf, tf.Duration(), got.Duration())
		}
	}
	if _, ok := domain.Timeframe("nonsense").Acquisition(); ok {
		t.Error("an unknown timeframe converted to an acquisition Timeframe")
	}
}

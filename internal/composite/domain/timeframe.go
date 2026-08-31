package domain

import (
	"fmt"
	"time"

	acq "github.com/agnos/agnoforge/internal/domain"
)

// Timeframe is the duration of one bar in this context, written in canonical
// short form. Unlike acquisition's fixed-duration Timeframe it also covers the
// calendar frames `1w` (Monday 00:00 UTC) and `1M` (the 1st at 00:00 UTC),
// which is the whole reason this context has its own type (ADR-0004).
//
// This file carries the identity of a Timeframe — its spelling, its canonical
// set, its order, and whether acquisition can express it. The boundary
// arithmetic lives in calendar.go, because declaring a Composite Dataset does
// not need it.
type Timeframe string

// The canonical composite timeframes, in ascending duration order.
const (
	TF1m  Timeframe = "1m"
	TF5m  Timeframe = "5m"
	TF15m Timeframe = "15m"
	TF30m Timeframe = "30m"
	TF1h  Timeframe = "1h"
	TF4h  Timeframe = "4h"
	TF1d  Timeframe = "1d"
	TF1w  Timeframe = "1w"
	TF1M  Timeframe = "1M"
)

// timeframes is the single source of truth, ordered by ascending duration.
// `1M` is longer than `1w` even though neither has a fixed length.
var timeframes = []Timeframe{TF1m, TF5m, TF15m, TF30m, TF1h, TF4h, TF1d, TF1w, TF1M}

// order maps each canonical Timeframe to its position in timeframes, which is
// how a set of them is sorted and deduplicated.
var order = func() map[Timeframe]int {
	m := make(map[Timeframe]int, len(timeframes))
	for i, tf := range timeframes {
		m[tf] = i
	}
	return m
}()

// Timeframes returns the canonical composite timeframes in ascending duration
// order. The caller receives a copy and may modify it freely.
func Timeframes() []Timeframe {
	out := make([]Timeframe, len(timeframes))
	copy(out, timeframes)
	return out
}

// ParseTimeframe accepts exactly the canonical composite timeframes and
// rejects everything else with an error wrapping ErrInvalidConfig. The
// spelling is case-sensitive: `1m` is a minute and `1M` is a month.
func ParseTimeframe(s string) (Timeframe, error) {
	tf := Timeframe(s)
	if _, ok := order[tf]; !ok {
		return "", fmt.Errorf("%w: unknown timeframe %q", ErrInvalidConfig, s)
	}
	return tf, nil
}

// Valid reports whether tf is one of the canonical composite timeframes.
func (tf Timeframe) Valid() bool {
	_, ok := order[tf]
	return ok
}

// Order is the Timeframe's position in ascending duration order. It is only
// meaningful for a canonical Timeframe.
func (tf Timeframe) Order() int { return order[tf] }

// String returns the canonical short form.
func (tf Timeframe) String() string { return string(tf) }

// acquisitionFrames maps every fixed composite Timeframe onto the acquisition
// Timeframe that means the same thing. It is deliberately the *only* bridge
// between the two contexts' timeframe vocabularies: the calendar frames `1w`
// and `1M` have no entry, so there is no value to return for them and no way
// to smuggle one across the port boundary (ADR-0004).
//
// It is also where "fixed" is defined for this context, and where the fixed
// frames get their length — taken from acquisition rather than restated, so
// the two contexts cannot drift apart about how long a `4h` bar is.
var acquisitionFrames = map[Timeframe]acq.Timeframe{
	TF1m:  acq.TF1m,
	TF5m:  acq.TF5m,
	TF15m: acq.TF15m,
	TF30m: acq.TF30m,
	TF1h:  acq.TF1h,
	TF4h:  acq.TF4h,
	TF1d:  acq.TF1d,
}

// Acquisition converts tf to acquisition's Timeframe. It reports false — and
// returns the zero acquisition Timeframe, which acquisition itself rejects —
// for the calendar frames `1w` and `1M`, which acquisition deliberately
// cannot express, and for anything that is not a canonical Timeframe.
func (tf Timeframe) Acquisition() (acq.Timeframe, bool) {
	a, ok := acquisitionFrames[tf]
	return a, ok
}

// Fixed reports whether one bar of tf always lasts the same time. The seven
// fixed frames (`1m`…`1d`) do; the calendar frames `1w` and `1M` do not, and
// neither does an unknown Timeframe.
func (tf Timeframe) Fixed() bool {
	_, ok := acquisitionFrames[tf]
	return ok
}

// Duration is the length of one bar of tf, or zero when tf has no constant
// length — a calendar frame or an unknown Timeframe. Ask Window for the
// length of one concrete calendar window.
func (tf Timeframe) Duration() time.Duration {
	return acquisitionFrames[tf].Duration()
}

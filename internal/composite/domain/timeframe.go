package domain

import "fmt"

// Timeframe is the duration of one bar in this context, written in canonical
// short form. Unlike acquisition's fixed-duration Timeframe it also covers the
// calendar frames `1w` (Monday 00:00 UTC) and `1M` (the 1st at 00:00 UTC),
// which is the whole reason this context has its own type (ADR-0004).
//
// This file carries the identity of a Timeframe — its spelling, its canonical
// set and its order. The boundary arithmetic that goes with a calendar frame
// is a separate concern and is not needed to declare a Composite Dataset.
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

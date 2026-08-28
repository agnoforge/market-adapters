package domain

import (
	"fmt"
	"time"
)

// Timeframe is the duration of one Bar, written in canonical short form.
// Only fixed-duration timeframes exist: 1s, 1w and 1M are deliberately absent
// because a variable-length Bar cannot be aligned to a boundary arithmetically.
type Timeframe string

// The canonical timeframes, in ascending duration order.
const (
	TF1m  Timeframe = "1m"
	TF3m  Timeframe = "3m"
	TF5m  Timeframe = "5m"
	TF15m Timeframe = "15m"
	TF30m Timeframe = "30m"
	TF1h  Timeframe = "1h"
	TF2h  Timeframe = "2h"
	TF4h  Timeframe = "4h"
	TF6h  Timeframe = "6h"
	TF8h  Timeframe = "8h"
	TF12h Timeframe = "12h"
	TF1d  Timeframe = "1d"
	TF3d  Timeframe = "3d"
)

// timeframes is the single source of truth, ordered by ascending duration.
var timeframes = []Timeframe{
	TF1m, TF3m, TF5m, TF15m, TF30m,
	TF1h, TF2h, TF4h, TF6h, TF8h, TF12h,
	TF1d, TF3d,
}

var timeframeDurations = map[Timeframe]time.Duration{
	TF1m:  1 * time.Minute,
	TF3m:  3 * time.Minute,
	TF5m:  5 * time.Minute,
	TF15m: 15 * time.Minute,
	TF30m: 30 * time.Minute,
	TF1h:  1 * time.Hour,
	TF2h:  2 * time.Hour,
	TF4h:  4 * time.Hour,
	TF6h:  6 * time.Hour,
	TF8h:  8 * time.Hour,
	TF12h: 12 * time.Hour,
	TF1d:  24 * time.Hour,
	TF3d:  72 * time.Hour,
}

// Timeframes returns the canonical timeframes in ascending duration order.
// The caller receives a copy and may modify it freely.
func Timeframes() []Timeframe {
	out := make([]Timeframe, len(timeframes))
	copy(out, timeframes)
	return out
}

// ParseTimeframe accepts exactly the canonical timeframes and rejects
// everything else with an error wrapping ErrUnsupportedTimeframe.
func ParseTimeframe(s string) (Timeframe, error) {
	tf := Timeframe(s)
	if _, ok := timeframeDurations[tf]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedTimeframe, s)
	}
	return tf, nil
}

// Duration is the length of one Bar at this Timeframe. An unsupported
// Timeframe has a zero duration.
func (tf Timeframe) Duration() time.Duration { return timeframeDurations[tf] }

// Valid reports whether tf is one of the canonical timeframes.
func (tf Timeframe) Valid() bool {
	_, ok := timeframeDurations[tf]
	return ok
}

// String returns the canonical short form.
func (tf Timeframe) String() string { return string(tf) }

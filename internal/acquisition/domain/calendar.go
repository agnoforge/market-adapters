package domain

import (
	"iter"
	"time"
)

// TradingCalendar is the Provider's statement of which Bars are expected for
// a Symbol over a range. Gap detection is expected-minus-present, so a
// market-closed period the calendar omits is not a Gap.
type TradingCalendar interface {
	// Expected yields, in ascending order, every open_time in [r.Start, r.End)
	// that falls on a tf boundary.
	Expected(r Range, tf Timeframe) iter.Seq[time.Time]
}

// Continuous is a 24/7 calendar: every Timeframe boundary in the range is an
// expected open_time. Binance uses it.
type Continuous struct{}

// Expected yields every tf boundary in [r.Start, r.End). Boundaries are
// multiples of the Timeframe duration measured from the Unix epoch, so
// r.Start is aligned up to the next boundary when it does not sit on one.
// An empty range or an unsupported Timeframe yields nothing.
func (Continuous) Expected(r Range, tf Timeframe) iter.Seq[time.Time] {
	return func(yield func(time.Time) bool) {
		step := tf.Duration().Milliseconds()
		if step <= 0 || r.IsEmpty() {
			return
		}
		end := r.End.UnixMilli()
		for t := alignUp(r.Start.UnixMilli(), step); t < end; t += step {
			if !yield(time.UnixMilli(t).UTC()) {
				return
			}
		}
	}
}

// alignUp rounds ms up to the next multiple of step measured from the Unix
// epoch, leaving it unchanged when it already is one. It is correct for
// instants before the epoch, where ms is negative.
func alignUp(ms, step int64) int64 {
	rem := ((ms % step) + step) % step
	if rem == 0 {
		return ms
	}
	return ms + (step - rem)
}

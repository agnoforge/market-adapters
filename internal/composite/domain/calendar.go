package domain

import (
	"iter"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
)

// Boundary arithmetic for a composite Timeframe. Every instant here is UTC:
// this context has no timezone configuration, and never will in v1.
//
// A fixed frame (`1m`…`1d`) is anchored exactly the way acquisition anchors
// it — at multiples of the frame's own duration measured from the Unix epoch —
// so the two contexts can never disagree about where a `1h` bar begins. The
// calendar frames cannot be anchored that way, which is the whole reason this
// context has its own Timeframe (ADR-0004): the epoch is a Thursday, so `1w`
// is not "7 days from the epoch". Instead `1w` starts on Monday 00:00 UTC
// (ISO-8601) and `1M` on the 1st at 00:00 UTC.

// WindowStart returns the open time of the tf window that contains t: the
// largest tf boundary that is not after t. The result is UTC whatever zone t
// carried. An unknown Timeframe has no boundaries and returns the zero time.
func (tf Timeframe) WindowStart(t time.Time) time.Time {
	u := t.UTC()
	switch tf {
	case TF1w:
		// ISO-8601: Monday is day 0 of the week.
		day := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	case TF1M:
		return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	step := tf.Duration().Milliseconds()
	if step <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(alignDown(u.UnixMilli(), step)).UTC()
}

// Window returns the half-open range [open, next open) of the tf window that
// contains t. Its length is the calendar's, not a constant: a `1M` window is
// 28, 29, 30 or 31 days long. An unknown Timeframe returns the empty Range.
func (tf Timeframe) Window(t time.Time) acq.Range {
	if !tf.Valid() {
		return acq.Range{}
	}
	start := tf.WindowStart(t)
	return acq.Range{Start: start, End: tf.nextWindowStart(start)}
}

// WindowStarts yields, in ascending order, the open time of every tf window
// that overlaps r — the windows that together cover r. The first one begins
// at or before r.Start, so a range that starts mid-window still has that
// window materialized (partially covered windows are flagged incomplete, never
// silently dropped); the last one begins before r.End. An empty range or an
// unknown Timeframe yields nothing.
func (tf Timeframe) WindowStarts(r acq.Range) iter.Seq[time.Time] {
	return func(yield func(time.Time) bool) {
		if !tf.Valid() || r.IsEmpty() {
			return
		}
		end := r.End.UTC()
		for s := tf.WindowStart(r.Start); s.Before(end); s = tf.nextWindowStart(s) {
			if !yield(s) {
				return
			}
		}
	}
}

// nextWindowStart advances one whole window from a window start. It is only
// correct for an instant that already is a boundary, which is why it is not
// exported: Window and WindowStarts both feed it one.
func (tf Timeframe) nextWindowStart(start time.Time) time.Time {
	switch tf {
	case TF1w:
		return start.AddDate(0, 0, 7)
	case TF1M:
		return start.AddDate(0, 1, 0)
	}
	return start.Add(tf.Duration())
}

// alignDown rounds ms down to the multiple of step measured from the Unix
// epoch, leaving it unchanged when it already is one. It floors rather than
// truncating, so it is correct before the epoch, where ms is negative. This is
// acquisition's alignUp read the other way; the two must stay mirror images.
func alignDown(ms, step int64) int64 {
	return ms - ((ms%step)+step)%step
}

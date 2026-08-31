package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// Higher-timeframe materialization: the one place in this service that derives
// a bar from other bars.
//
// It happens as SQL against acquisition's own table, read-only (decision 33,
// ADR-0005). The source bars are the largest data in the system and they are
// already in this file; staging them through Go values only to aggregate them
// back would copy all of it for ceremony. What the aggregation must never do is
// round: every price stays in the DECIMAL(20,8) it is stored in, from the
// source row to the derived one. Not one expression below converts a price to a
// float, and that is the whole reason first/last are `arg_min`/`arg_max` over
// the open time rather than anything that would re-type the value.
//
// ponytail: the aggregation is SQL because the data is already there. If the
// derivation ever needs a rule SQL cannot state — a session calendar, a
// provider-weighted merge — move it into a Go aggregator over a streamed read
// of the same rows; the port signature would not change.

// Materialize replaces a Composite Dataset's materialized higher-timeframe
// bars with the ones its Timeframes derive from the composite 1-minute
// timeline, and reports how many bars it wrote.
//
// Everything happens in one transaction: every window the dataset had is
// replaced by what this Materialization derives, so a timeframe it does not
// name — or a window a shortened timeline no longer reaches — is gone rather
// than left behind. The empty Materialization, which is what a Build that could
// not be ready hands over, therefore leaves no derived bars at all.
//
// Re-running it over the same timeline converges: the same source bars are
// aggregated by the same rules, and the bars that come out are identical down
// to the last decimal.
func (s *Store) Materialize(ctx context.Context, name domain.Name, m app.Materialization) (int64, error) {
	var written int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// A Build derives the whole timeline, so every window of the dataset is
		// an affected window: they all go, and what this Materialization derives
		// takes their place. A frame the Build no longer materializes, and a
		// window a shortened timeline no longer reaches, therefore leave nothing
		// behind — what is stored always describes the build it came from.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM composite_materialized_bars WHERE dataset = ?`, name.String()); err != nil {
			return fmt.Errorf("composite duckdb: materializing %q: %w", name, err)
		}
		if len(m.Segments) == 0 {
			// No timeline, nothing to derive from: the dataset is left with no
			// derived bars, which is what a Build that could not be ready leaves.
			return nil
		}
		for _, tf := range m.Timeframes {
			n, err := materializeTimeframe(ctx, tx, name, tf, m)
			if err != nil {
				return err
			}
			written += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// materializeTimeframe derives one timeframe's bars from the timeline and
// writes them.
func materializeTimeframe(ctx context.Context, tx *sql.Tx, name domain.Name,
	tf domain.Timeframe, m app.Materialization) (int64, error) {
	statement, args, err := aggregation(name, tf, m)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return 0, materializeErr(name, tf, err)
	}
	written, err := res.RowsAffected()
	if err != nil {
		return 0, materializeErr(name, tf, err)
	}
	return written, nil
}

// aggregation is the derivation itself: one INSERT that reads the source bars
// of every Segment, groups them into the windows of tf, and writes one derived
// bar per window that holds at least one source bar.
//
// The four price aggregates are the standard OHLC rules — first open, highest
// high, lowest low, last close — and the volume is their sum. `arg_min` and
// `arg_max` answer "the open of the earliest bar" and "the close of the latest"
// over the open time, which is the only ordering a composite timeline has; both
// return the DECIMAL they were given. Nothing here is a float, so a price that
// went into the database exact comes out exact.
func aggregation(name domain.Name, tf domain.Timeframe, m app.Materialization) (string, []any, error) {
	window, err := windowExpression(tf)
	if err != nil {
		return "", nil, err
	}
	sources, args, err := sourceBars(m.Segments)
	if err != nil {
		return "", nil, err
	}
	statement := `
		INSERT INTO composite_materialized_bars
			(dataset, timeframe, open_time, "open", high, low, "close", volume, mat_version)
		SELECT ?, ?, window_open,
		       arg_min("open", open_time), max(high), min(low), arg_max("close", open_time),
		       sum(volume), ?
		FROM (SELECT ` + window + ` AS window_open, open_time, "open", high, low, "close", volume
		      FROM (` + sources + `))
		GROUP BY window_open`
	return statement, append([]any{name.String(), tf.String(), m.Version}, args...), nil
}

// sourceBars is the composite 1-minute timeline as SQL: the source bars each
// Segment answers for, and nothing else. A bar the source has outside the
// Segment's range is not part of this dataset's timeline and is never read.
//
// This is the read-only read of acquisition's own table (ADR-0005): the
// composite context selects from `bars`, and writes nothing there, ever.
func sourceBars(segments []domain.Segment) (string, []any, error) {
	selects := make([]string, 0, len(segments))
	args := make([]any, 0, len(segments)*5)
	for _, seg := range segments {
		timeframe, ok := seg.Source.Timeframe.Acquisition()
		if !ok {
			return "", nil, fmt.Errorf("%w: acquisition has no timeframe %q",
				domain.ErrInvalidConfig, seg.Source.Timeframe)
		}
		selects = append(selects, `
			SELECT open_time, "open", high, low, "close", volume FROM bars
			WHERE provider = ? AND symbol = ? AND timeframe = ?
			  AND open_time >= ? AND open_time < ?`)
		args = append(args, seg.Source.Provider, seg.Source.Symbol.String(), timeframe.String(),
			seg.Range.Start.UTC().UnixMilli(), seg.Range.End.UTC().UnixMilli())
	}
	return strings.Join(selects, "\n UNION ALL "), args, nil
}

// windowExpression is the open time of the tf window a source bar falls in,
// as SQL over the bar's own open time in epoch milliseconds.
//
// It must agree with the domain's WindowStart for every instant, and it is
// written to be the same arithmetic (ADR-0004):
//
//   - A fixed frame is anchored at multiples of its own length from the Unix
//     epoch — the floored remainder, so it is right before the epoch too, where
//     the epoch milliseconds are negative.
//   - `1w` and `1M` cannot be anchored that way: the epoch is a Thursday. They
//     are the calendar's own boundaries, and DuckDB's date_trunc states them —
//     its weeks start on Monday, ISO-8601's Monday, which is the boundary this
//     context defines. The store tests pin that agreement against the domain's
//     arithmetic rather than trusting it.
//
// Everything here is integer and date arithmetic over UTC. There is no
// timezone: the composite context has none, and epoch_ms yields the UTC
// wall clock a bar's open time is.
func windowExpression(tf domain.Timeframe) (string, error) {
	switch tf {
	case domain.TF1w:
		return "epoch_ms(date_trunc('week', epoch_ms(open_time)))", nil
	case domain.TF1M:
		return "epoch_ms(date_trunc('month', epoch_ms(open_time)))", nil
	}
	step := tf.Duration().Milliseconds()
	if step <= 0 {
		return "", fmt.Errorf("%w: %q has no window to aggregate on", domain.ErrInvalidConfig, tf)
	}
	// The step is one of a closed vocabulary of nine timeframes, never anything
	// a caller spelled, so it is arithmetic in the statement rather than a bound
	// parameter — a grouping expression cannot be parameterised anyway.
	return fmt.Sprintf("open_time - ((open_time %% %d) + %d) %% %d", step, step, step), nil
}

// materializeErr names which dataset's which timeframe could not be derived.
func materializeErr(name domain.Name, tf domain.Timeframe, err error) error {
	return fmt.Errorf("composite duckdb: materializing the %s bars of %q: %w", tf, name, err)
}

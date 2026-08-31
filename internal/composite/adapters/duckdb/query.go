package duckdb

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The read side of the consumer contract: the bars of one Composite Dataset,
// at one Timeframe, over one half-open range.
//
// Both resolutions come out of the same file and the same statement shape. The
// 1-minute timeline is the source bars each Segment answers for, read-only from
// acquisition's own table (decision 33, ADR-0005), clipped to the query's range
// and unioned in timeline order; every higher frame is the derived bars a Build
// materialized. Nothing is aggregated here — a query never derives a bar, it
// reads one — and nothing is rounded: the five prices stay in the
// DECIMAL(20,8) they are stored in, all the way to the wire.

// Bars answers one resolved bars query, ascending by open time, with the prices
// as the exact decimal text the database holds. Reading them as text is the
// point: a float anywhere on this path would be visible in the last decimals.
func (s *Store) Bars(ctx context.Context, q app.BarQuery) ([]acq.Bar, error) {
	source, err := barSource(q)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT open_time,
		       CAST("open" AS VARCHAR), CAST(high AS VARCHAR), CAST(low AS VARCHAR),
		       CAST("close" AS VARCHAR), CAST(volume AS VARCHAR)
		FROM (`+source+`)
		ORDER BY open_time`)
	if err != nil {
		return nil, barsErr(q, err)
	}
	defer rows.Close()

	out := make([]acq.Bar, 0)
	for rows.Next() {
		var (
			b  acq.Bar
			ms int64
		)
		if err := rows.Scan(&ms, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			return nil, barsErr(q, err)
		}
		b.OpenTime = time.UnixMilli(ms).UTC()
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, barsErr(q, err)
	}
	return out, nil
}

// ExportBars streams the same bars to w as a Parquet file, written by DuckDB's
// own COPY … TO, so the encoding is DuckDB's and not ours — the same writer,
// and therefore the same file layout, acquisition's own export uses.
//
// Columns: open_time BIGINT (UTC epoch milliseconds), then open, high, low,
// close and volume as DECIMAL(20,8). The dataset and the timeframe are not
// repeated in the file: every bar in it belongs to the query that produced it.
func (s *Store) ExportBars(ctx context.Context, q app.BarQuery, w io.Writer) error {
	source, err := barSource(q)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "agnoforge-composite-parquet-")
	if err != nil {
		return barsErr(q, err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "bars.parquet")

	statement := fmt.Sprintf(`
		COPY (
			SELECT open_time, "open", high, low, "close", volume
			FROM (%s)
			ORDER BY open_time
		) TO %s (FORMAT PARQUET)`, source, quote(path))
	if _, err := s.db.ExecContext(ctx, statement); err != nil {
		return barsErr(q, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return barsErr(q, err)
	}
	defer file.Close()

	if _, err := io.Copy(w, file); err != nil {
		return barsErr(q, err)
	}
	return nil
}

// barSource is the query as one SELECT of raw bar columns, before ordering:
// the derived bars of a materialized frame, or the source bars of the 1-minute
// timeline across the Segments.
//
// Every value in it is a literal rather than a bound parameter, because COPY …
// TO takes neither its destination nor its predicates as one — the same reason
// acquisition's own export inlines them — and the two paths must read exactly
// the same rows. Everything inlined is either an integer this package computed
// or a string quote escapes.
func barSource(q app.BarQuery) (string, error) {
	if q.Derived() {
		return derivedBars(q), nil
	}
	return timelineBars(q)
}

// derivedBars reads one materialized frame of one dataset. The materialization
// version is not a predicate: bars derived by an older version are still the
// bars this dataset was built with, and the dataset itself reads as stale —
// which is where that fact belongs, rather than in a silently empty answer.
func derivedBars(q app.BarQuery) string {
	start, end := msBounds(q.Range)
	return fmt.Sprintf(`
		SELECT open_time, "open", high, low, "close", volume
		FROM composite_materialized_bars
		WHERE dataset = %s AND timeframe = %s
		  AND open_time >= %d AND open_time < %d`,
		quote(q.Name().String()), quote(q.Timeframe.String()), start, end)
}

// timelineBars is the composite 1-minute timeline as SQL: the source bars each
// Segment answers for, clipped to the queried range and unioned in the order
// the Segments run.
//
// This is the read-only read of acquisition's own table (ADR-0005). A bar the
// source has outside a Segment's range is not part of this dataset's timeline
// and is never read, so two providers holding data for the same instants can
// never both reach a consumer.
func timelineBars(q app.BarQuery) (string, error) {
	selects := make([]string, 0, len(q.Segments))
	for _, seg := range q.Segments {
		clipped := seg.Range.Intersect(q.Range)
		if clipped.IsEmpty() {
			continue
		}
		timeframe, ok := seg.Source.Timeframe.Acquisition()
		if !ok {
			return "", fmt.Errorf("%w: acquisition has no timeframe %q",
				domain.ErrInvalidConfig, seg.Source.Timeframe)
		}
		start, end := msBounds(clipped)
		selects = append(selects, fmt.Sprintf(`
			SELECT open_time, "open", high, low, "close", volume FROM bars
			WHERE provider = %s AND symbol = %s AND timeframe = %s
			  AND open_time >= %d AND open_time < %d`,
			quote(seg.Source.Provider), quote(seg.Source.Symbol.String()),
			quote(timeframe.String()), start, end))
	}
	if len(selects) == 0 {
		// The queried range falls outside every Segment. The answer is no bars,
		// and it is still a well-typed one: the columns come from the table the
		// timeline is made of, so an empty JSON answer and an empty Parquet file
		// have the schema every other answer has.
		return `SELECT open_time, "open", high, low, "close", volume FROM bars WHERE false`, nil
	}
	return strings.Join(selects, "\n UNION ALL "), nil
}

// msBounds is a half-open range as the pair of epoch-millisecond bounds every
// bar table stores.
func msBounds(r acq.Range) (int64, int64) {
	return r.Start.UTC().UnixMilli(), r.End.UTC().UnixMilli()
}

// quote renders s as a SQL string literal, escaping the one character that
// could end it.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// barsErr names which dataset's which frame over which range could not be read.
func barsErr(q app.BarQuery, err error) error {
	return fmt.Errorf("composite duckdb: the %s bars of %q over %s: %w",
		q.Timeframe, q.Name(), q.Range, err)
}

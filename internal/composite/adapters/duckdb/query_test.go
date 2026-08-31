package duckdb_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// The read side against a real database: one query surface over two very
// different sources of rows — the referenced source bars of the 1-minute
// timeline, and the derived bars of a materialized frame — answering with the
// same shape and the same exact decimals in JSON values and in Parquet.

// queried is one bar as a query answers it: the open time, and the five prices
// as the exact decimal text they were stored as.
type queriedBar struct {
	OpenTime                       time.Time
	Open, High, Low, Close, Volume string
}

func (b queriedBar) String() string {
	return fmt.Sprintf("%s o=%s h=%s l=%s c=%s v=%s",
		b.OpenTime.UTC().Format(time.RFC3339), b.Open, b.High, b.Low, b.Close, b.Volume)
}

// query is one resolved bars query over the fixture's dataset.
func (f *fixture) query(tf domain.Timeframe, segments []domain.Segment, r acq.Range) compositeapp.BarQuery {
	d, err := f.store.Dataset(context.Background(), "btc-usd")
	if err != nil {
		f.t.Fatalf("Dataset: %v", err)
	}
	return compositeapp.BarQuery{Dataset: d, Timeframe: tf, Range: r, Segments: segments}
}

// queried reads the bars one query answers with.
func (f *fixture) queried(q compositeapp.BarQuery) []queriedBar {
	f.t.Helper()
	bars, err := f.store.Bars(context.Background(), q)
	if err != nil {
		f.t.Fatalf("Bars: %v", err)
	}
	out := make([]queriedBar, 0, len(bars))
	for _, b := range bars {
		out = append(out, queriedBar{
			OpenTime: b.OpenTime, Open: b.Open, High: b.High,
			Low: b.Low, Close: b.Close, Volume: b.Volume,
		})
	}
	return out
}

// exported streams the same query to Parquet and reads the file back through a
// second, independent DuckDB — which is how another service consumes an export.
// The prices come back as text so a float anywhere on the path would show.
func (f *fixture) exported(q compositeapp.BarQuery) []queriedBar {
	f.t.Helper()
	var buf bytes.Buffer
	if err := f.store.ExportBars(context.Background(), q, &buf); err != nil {
		f.t.Fatalf("ExportBars: %v", err)
	}
	if buf.Len() == 0 {
		f.t.Fatal("ExportBars wrote nothing")
	}
	path := filepath.Join(f.t.TempDir(), "bars.parquet")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		f.t.Fatalf("writing the export: %v", err)
	}

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		f.t.Fatalf("second DuckDB: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT open_time, CAST("open" AS VARCHAR), CAST(high AS VARCHAR), CAST(low AS VARCHAR),
		       CAST("close" AS VARCHAR), CAST(volume AS VARCHAR)
		FROM read_parquet('` + path + `') ORDER BY open_time`)
	if err != nil {
		f.t.Fatalf("reading the export back: %v", err)
	}
	defer rows.Close()

	out := make([]queriedBar, 0)
	for rows.Next() {
		var (
			b  queriedBar
			ms int64
		)
		if err := rows.Scan(&ms, &b.Open, &b.High, &b.Low, &b.Close, &b.Volume); err != nil {
			f.t.Fatalf("scanning an exported bar: %v", err)
		}
		b.OpenTime = time.UnixMilli(ms).UTC()
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("reading the export back: %v", err)
	}
	return out
}

// assertQueried insists on exactly these bars, in this order, decimal for
// decimal.
func assertQueried(t *testing.T, got, want []queriedBar) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("bars = %v,\nwant %v", got, want)
	}
	for i := range want {
		if !got[i].OpenTime.Equal(want[i].OpenTime) || got[i] != want[i] {
			t.Fatalf("bar %d = %s, want %s", i, got[i], want[i])
		}
	}
}

// numbered is one source bar whose every price names its own minute, so a
// query that read the wrong minutes could not possibly pass.
func numbered(i int) acq.Bar {
	return acq.Bar{
		Open:   fmt.Sprintf("100.0000000%d", i%10),
		High:   fmt.Sprintf("101.0000000%d", i%10),
		Low:    fmt.Sprintf("99.0000000%d", i%10),
		Close:  fmt.Sprintf("100.5000000%d", i%10),
		Volume: "0.10000000",
	}
}

// TestTheOneMinuteTimelineIsReadFromTheSourceBars: the 1m query is the
// referenced source bars of the Segment, read-only out of acquisition's own
// table, decimal-exact and in time order.
func TestTheOneMinuteTimelineIsReadFromTheSourceBars(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(5 * time.Minute)}
	f.seed(baseSrc, r, numbered)

	got := f.queried(f.query(domain.TF1m, base(r), r))
	want := make([]queriedBar, 0, 5)
	for i := 0; i < 5; i++ {
		b := numbered(i)
		want = append(want, queriedBar{
			OpenTime: start.Add(time.Duration(i) * time.Minute),
			Open:     b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume,
		})
	}
	assertQueried(t, got, want)
	assertQueried(t, f.exported(f.query(domain.TF1m, base(r), r)), want)
}

// TestTheOneMinuteTimelineIsReadAcrossTheSegmentsInOrder: two providers, two
// Segments, one continuous answer — and not one bar from outside a Segment's
// own range, even though both providers hold data for the whole hour.
func TestTheOneMinuteTimelineIsReadAcrossTheSegmentsInOrder(t *testing.T) {
	f := newFixture(t)
	other := domain.Source{
		Instrument: "BTC/USD", Provider: "coinbase",
		Symbol: acq.Symbol("BTC-USD"), Timeframe: domain.TF1m,
	}
	whole := acq.Range{Start: start, End: start.Add(10 * time.Minute)}
	f.seed(baseSrc, whole, func(int) acq.Bar {
		return acq.Bar{Open: "100.00000000", High: "100.00000000", Low: "100.00000000", Close: "100.00000000", Volume: "1.00000000"}
	})
	f.seed(other, whole, func(int) acq.Bar {
		return acq.Bar{Open: "200.00000000", High: "200.00000000", Low: "200.00000000", Close: "200.00000000", Volume: "1.00000000"}
	})

	segments := []domain.Segment{
		{Kind: domain.SegmentBase, Source: baseSrc, Range: acq.Range{Start: start, End: start.Add(5 * time.Minute)}},
		{Kind: domain.SegmentCatchUp, Source: other, Range: acq.Range{Start: start.Add(5 * time.Minute), End: start.Add(10 * time.Minute)}},
	}
	got := f.queried(f.query(domain.TF1m, segments, whole))
	if len(got) != 10 {
		t.Fatalf("bars = %v, want the ten minutes of the timeline", got)
	}
	for i, b := range got {
		want := "100.00000000"
		if i >= 5 {
			want = "200.00000000"
		}
		if b.Open != want || !b.OpenTime.Equal(start.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("bar %d = %s, want %s at minute %d", i, b, want, i)
		}
	}
}

// TestAQueryNarrowerThanTheTimelineReadsOnlyTheRangeAsked: the range is
// half-open and it is honoured on both ends.
func TestAQueryNarrowerThanTheTimelineReadsOnlyTheRangeAsked(t *testing.T) {
	f := newFixture(t)
	timeline := acq.Range{Start: start, End: start.Add(10 * time.Minute)}
	f.seed(baseSrc, timeline, numbered)

	asked := acq.Range{Start: start.Add(2 * time.Minute), End: start.Add(5 * time.Minute)}
	got := f.queried(f.query(domain.TF1m, base(timeline), asked))
	if len(got) != 3 {
		t.Fatalf("bars = %v, want minutes 2, 3 and 4", got)
	}
	if !got[0].OpenTime.Equal(asked.Start) || !got[2].OpenTime.Equal(start.Add(4*time.Minute)) {
		t.Errorf("bars = %v, want the half-open [2, 5) minutes", got)
	}
}

// TestAQueryOutsideTheTimelineIsNoBarsAndStillAValidExport: a range no Segment
// reaches answers with nothing, and the Parquet file it writes is still a
// readable one with the same columns.
func TestAQueryOutsideTheTimelineIsNoBarsAndStillAValidExport(t *testing.T) {
	f := newFixture(t)
	timeline := acq.Range{Start: start, End: start.Add(10 * time.Minute)}
	f.seed(baseSrc, timeline, numbered)

	elsewhere := acq.Range{Start: start.Add(time.Hour), End: start.Add(2 * time.Hour)}
	q := f.query(domain.TF1m, base(timeline), elsewhere)
	if got := f.queried(q); len(got) != 0 {
		t.Fatalf("bars = %v, want none", got)
	}
	if got := f.exported(q); len(got) != 0 {
		t.Fatalf("exported bars = %v, want an empty but readable file", got)
	}
}

// TestAHigherFrameIsReadFromTheMaterializedBars: the derived bars a Build
// wrote, exactly as the materialization tests expect them, over the same query
// surface as the 1-minute timeline.
func TestAHigherFrameIsReadFromTheMaterializedBars(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(10 * time.Minute)}
	f.seed(baseSrc, r, func(i int) acq.Bar {
		return acq.Bar{
			Open:   fmt.Sprintf("100.0000000%d", i),
			High:   fmt.Sprintf("101.0000000%d", (i+3)%10),
			Low:    fmt.Sprintf("99.0000000%d", (i+7)%10),
			Close:  fmt.Sprintf("100.5000000%d", i),
			Volume: "0.10000000",
		}
	})
	f.materialize(base(r), domain.TF5m)

	// The same two bars TestMaterializeDerivesEachWindowExactly pins, now read
	// back through the query surface a backtester uses.
	want := []queriedBar{
		{
			OpenTime: start,
			Open:     "100.00000000", High: "101.00000007", Low: "99.00000000",
			Close: "100.50000004", Volume: "0.50000000",
		},
		{
			OpenTime: start.Add(5 * time.Minute),
			Open:     "100.00000005", High: "101.00000009", Low: "99.00000002",
			Close: "100.50000009", Volume: "0.50000000",
		},
	}
	q := f.query(domain.TF5m, base(r), r)
	assertQueried(t, f.queried(q), want)
	assertQueried(t, f.exported(q), want)
}

// TestTheDerivedQueryHonoursItsRange: the same half-open rule over the derived
// bars, on window open times.
func TestTheDerivedQueryHonoursItsRange(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(baseSrc, r, flatBar)
	f.materialize(base(r), domain.TF5m)

	asked := acq.Range{Start: start.Add(5 * time.Minute), End: start.Add(20 * time.Minute)}
	got := f.queried(f.query(domain.TF5m, base(r), asked))
	if len(got) != 3 {
		t.Fatalf("bars = %v, want the windows at :05, :10 and :15", got)
	}
	if !got[0].OpenTime.Equal(asked.Start) || !got[2].OpenTime.Equal(start.Add(15*time.Minute)) {
		t.Errorf("bars = %v, want the half-open [05, 20) windows", got)
	}
}

// TestAnExportCarriesTheAcquisitionColumnsAndTypes: the wire shape of a
// composite export is the wire shape of an acquisition one — same column names,
// same types, decimals and not floats — so a consumer reads both the same way.
func TestAnExportCarriesTheAcquisitionColumnsAndTypes(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(baseSrc, r, flatBar)
	f.materialize(base(r), domain.TF5m)

	for _, tf := range []domain.Timeframe{domain.TF1m, domain.TF5m} {
		t.Run(tf.String(), func(t *testing.T) {
			var buf bytes.Buffer
			if err := f.store.ExportBars(context.Background(), f.query(tf, base(r), r), &buf); err != nil {
				t.Fatalf("ExportBars: %v", err)
			}
			path := filepath.Join(t.TempDir(), "bars.parquet")
			if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
				t.Fatalf("writing the export: %v", err)
			}
			names, types := describe(t, path)
			wantNames := []string{"open_time", "open", "high", "low", "close", "volume"}
			wantTypes := []string{"BIGINT", "DECIMAL(20,8)", "DECIMAL(20,8)", "DECIMAL(20,8)", "DECIMAL(20,8)", "DECIMAL(20,8)"}
			if len(names) != len(wantNames) {
				t.Fatalf("exported columns %v, want %v", names, wantNames)
			}
			for i := range wantNames {
				if names[i] != wantNames[i] {
					t.Errorf("column %d is %q, want %q", i, names[i], wantNames[i])
				}
				if types[i] != wantTypes[i] {
					t.Errorf("column %q is %s, want %s", names[i], types[i], wantTypes[i])
				}
			}
		})
	}
}

// TestAQueryLeavesNoTemporaryFile: the Parquet export writes through a
// temporary directory and takes it with it.
func TestAQueryLeavesNoTemporaryFile(t *testing.T) {
	f := newFixture(t)
	r := acq.Range{Start: start, End: start.Add(time.Hour)}
	f.seed(baseSrc, r, flatBar)

	pattern := filepath.Join(os.TempDir(), "agnoforge-composite-parquet-*")
	before, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var buf bytes.Buffer
	if err := f.store.ExportBars(context.Background(), f.query(domain.TF1m, base(r), r), &buf); err != nil {
		t.Fatalf("ExportBars: %v", err)
	}
	after, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("ExportBars left %d temporary directories behind", len(after)-len(before))
	}
}

// describe reads the column names and types of a Parquet file through a
// database of its own.
func describe(t *testing.T, path string) (names, types []string) {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("second DuckDB: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`DESCRIBE SELECT * FROM read_parquet('` + path + `')`)
	if err != nil {
		t.Fatalf("describing the export: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		columns, err := rows.Columns()
		if err != nil {
			t.Fatalf("columns: %v", err)
		}
		cells := make([]any, len(columns))
		holders := make([]any, len(columns))
		for i := range cells {
			holders[i] = &cells[i]
		}
		if err := rows.Scan(holders...); err != nil {
			t.Fatalf("scanning DESCRIBE: %v", err)
		}
		names = append(names, cells[0].(string))
		types = append(types, cells[1].(string))
	}
	return names, types
}

# Assumptions (spec silent)

- Module path: `github.com/agnos/agnoforge`.

## Ticket 01 (domain + skeleton)

- `Range` operations normalise their results to UTC, so returned ranges compare
  equal regardless of the location the caller's inputs carried.
- `Union` treats an empty range as an identity: `empty ∪ r = r`, ok. Only
  disjoint non-empty ranges report `false`.
- `Intersect` returns the zero `Range` (not an error) when there is no overlap.
- `Subtract` / `MergeRanges` / `SubtractRanges` return `nil`, not an empty
  slice, when nothing remains.
- `Bar.Validate` also rejects a zero or non-UTC `OpenTime`, and requires
  `Volume >= 0` (prices must be strictly positive). Parsing is `math/big.Rat`
  so a `DECIMAL(20,8)` value never loses precision in a comparison.
- `Bar.Validate` and `ParseGapStatus` return plain descriptive errors; the four
  sentinels in `errors.go` are the only ones the ticket calls for.
- `Timeframe.Duration()` returns `0` for an unsupported timeframe rather than
  panicking, and `Continuous.Expected` yields nothing for one.
- Calendar boundaries are computed in Unix **milliseconds** (spec: open_time is
  UTC epoch ms), which also avoids the year-2262 `UnixNano` overflow.

## Ticket 02 (DuckDB store adapter)

- Driver: `github.com/duckdb/duckdb-go/v2` v2.10505.0 (DuckDB 1.5.5), the only
  direct dependency. Driver name `duckdb`; `sql.Open("duckdb", ":memory:")` is
  an ephemeral database, any other string is a file path DuckDB creates.
- **Canonical decimal string**: DuckDB returns a `DECIMAL(20,8)` column as a
  `duckdb.Decimal`, which `database/sql` refuses to scan into `string` or
  `[]byte`. Prices are therefore read back as `CAST(col AS VARCHAR)`, which
  renders at the column's full scale: a plain (non-exponent) decimal with
  **exactly eight fractional digits**, e.g. `9.5` stored comes back
  `9.50000000`. Values still bind *in* as strings. A Bar that goes through the
  store is equal in value but not always byte-equal in spelling.
- `UpsertBars` re-runs `Bar.Validate` on every Bar and fails the whole batch on
  the first invalid one, writing nothing. The store is the last boundary before
  bytes become durable and the check is cheap next to the insert; adapters
  upstream still drop-and-log rather than relying on this.
- Prices are stored `NOT NULL`; the schema has no nullable OHLCV column,
  because a Bar without a price is not a Bar.
- `ExtendCoverage` and `ReplaceOpenGaps` rewrite through `domain.MergeRanges`
  rather than doing range arithmetic in SQL, so the merge rule lives in exactly
  one place. Both run in a single transaction.
- "Intersecting" for `ReplaceOpenGaps` and the `GapFilter` range is half-open
  overlap (`start_ms < r.End AND end_ms > r.Start`): a Gap that merely *touches*
  the range shares no instant with it and survives.
- `ReplaceOpenGaps` ignores the `ID` on incoming Gaps; ids come from the
  `gap_ids` sequence, since these are new records. An empty range deletes
  nothing and only inserts.
- `SetGapStatus` rejects a status outside the canonical four via
  `domain.ParseGapStatus`, and reports `domain.ErrNotFound` when no Gap has
  that id.
- **Parquet export layout**: `COPY (SELECT …) TO <tmp> (FORMAT PARQUET)` writes
  six columns, in order: `open_time BIGINT` (UTC epoch milliseconds), then
  `open`, `high`, `low`, `close`, `volume`, all `DECIMAL(20,8)`. The Dataset is
  *not* repeated per Bar — the file is one Dataset by construction. The file is
  written under `os.MkdirTemp`, streamed to the writer, and the directory is
  removed. `COPY` takes neither its destination nor its predicates as bound
  parameters, so those few values are escaped as SQL string literals.
- Exporting a range with no Bars yields a valid, empty Parquet file rather than
  an error.
- Batched multi-row `INSERT OR REPLACE` (500 Bars per statement) is used
  instead of the DuckDB Appender: 44,640 Bars — one January of `1m` — upsert in
  ~1.6s, which is far below the network cost of acquiring them, so the
  Appender's complexity is not yet earned.
- `*Store` carries a `Bars(ctx, id, r)` reader that is **not** on the `Store`
  port. The port has no way to read a Bar back, so nothing could otherwise
  verify that an upsert overwrote a value; it is also what the spec's
  `bars?format=json` path will need.
- Tests use `:memory:` only. `TestMain` fails the package when a run leaves a
  `*.duckdb`/`*.parquet`/`*.db`/`*.wal` file beside the package or at the
  repository root; the exported Parquet bytes are written into `t.TempDir()`.
  Schema probing from the external `duckdb_test` package goes through
  `export_test.go`, which is compiled only under test and does not widen the
  adapter's API.

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

## Ticket 03 (Binance klines adapter)

- **`EarliestAvailable` asks at `1m`.** The port takes no Timeframe, so the
  probe (`startTime=0&limit=1`) uses the finest canonical Timeframe and
  reports that open_time. A coarser Timeframe's first bar can only open later,
  so clipping `Bars`'s start to this value never skips a Bar.
- **Zero rows from the probe is an unknown Symbol.** Binance answers a
  well-formed request for a Symbol it has no history for with `[]`; that is
  reported as `domain.ErrUnknownSymbol` rather than a bare error, since the
  caller can act on nothing else.
- **`endTime` is inclusive on the wire**, a `Range` is half-open, so the page
  request sends `end.UnixMilli()-1`. Rows are additionally filtered to
  `[start, end)` on open_time, so a Provider that ignores the bound cannot
  widen the result.
- **Clipping the end**: `end = min(r.End, floor(now, tf))`. `floor(now, tf)`
  is the open_time of the bar still forming, and a bar opening strictly before
  it has already closed, so that instant is exactly the exclusive end of the
  fully closed bars. Rows whose `closeTime >= now` are dropped as well — the
  two rules agree, and the second holds even if the Provider misreports a
  boundary.
- **Paging advances by `last open_time + tf`**, where `last` is the greatest
  open_time in the raw page including rows that were dropped. That is what
  makes "never a duplicate, never a skip" hold when a page's tail is filtered.
  A page shorter than 1000 rows, an empty page, or a page that fails to
  advance ends the sequence.
- **An empty page is not yielded.** A page whose rows were all filtered or
  dropped yields nothing rather than an empty slice, so a consumer's "each
  yielded page = upsert + ExtendCoverage" never runs on nothing.
- **Rate-limit weight is spent per attempt, not per logical request**: a retry
  is another request against the Provider's budget, so it is charged again.
- **Backoff doubles only when the Provider gave no instruction.** A
  `Retry-After` replaces that attempt's delay and leaves the doubling sequence
  where it was; the sequence for five straight failures is 500ms, 1s, 2s, 4s.
- **`Retry-After` is read as whole seconds only.** The HTTP-date form is not
  used by Binance; anything unparseable falls back to the backoff.
- **418 is treated as 429.** It is Binance's ban after ignored rate limits and
  carries the same header.
- **Every 4xx that is not 429/418 is permanent**, whether or not the body
  carries a Binance code; only `-1121` maps to `domain.ErrUnknownSymbol`.
- **The bucket refills continuously** (6000 weight / 60s = 100 weight/s) rather
  than in per-minute windows, so a run cannot burst 12000 across a window
  boundary. A waiter re-checks after sleeping instead of holding a
  reservation, which is fair enough for one process and needs no queue.
- **`binance.BaseURL()`** reads `BINANCE_BASE_URL` and falls back to
  `DefaultBaseURL`. `New` still takes the base URL as an argument — the helper
  only names where the override lives, so wiring and tests agree on it.
- **Tests cannot reach the network by construction**, not by convention:
  `TestMain` replaces `http.DefaultTransport` with one that refuses any
  non-loopback host, so even a default `http.Client` built inside `New` is
  blocked, and `TestDefaultBaseURLIsUnreachableFromATest` asserts that.
- **The fake clock only moves when something sleeps on it**, which is what
  makes both the backoff sequence and the token bucket's 20ms refill wait
  exactly assertable instead of timing-dependent.

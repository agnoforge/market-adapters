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

## Ticket 04 (Backfill use case)

- **A permanent Provider failure at Start is an error, not a `failed`
  record.** The spec says an unknown Symbol / unsupported Timeframe makes the
  Backfill "failed immediately"; nothing is registered, so `StartBackfill`
  returns the error (`ErrUnknownProvider`, `domain.ErrUnsupportedTimeframe`,
  `domain.ErrUnknownSymbol`) and there is no id to poll. That is what the HTTP
  layer needs for a 400 — a 202 carrying an id that is already `failed` would
  be a worse answer.
- **`app.ErrUnknownProvider`** is new, and lives in `internal/app`: the set of
  Providers is a property of the wiring, not of the domain.
- **The effective range is `[max(requested start, EarliestAvailable),
  requested end)`.** Clipping the end down to the last closed Bar stays in the
  adapter (`Provider.Bars` does it); the app reports the range it knows, which
  can therefore extend past the last closed Bar.
- **The run's context is derived from `context.Background()`, not from the
  request.** The HTTP request that starts a Backfill ends immediately, so
  inheriting its context would cancel every Backfill on response.
- **Terminal work runs on its own `context.Background()`**: a cancelled
  Backfill must still record what it landed and detect the Gaps inside it.
- **A completed Backfill's Coverage is the whole effective range**, extended
  once at the end, even for minutes the Provider had no Bar for — those become
  Gaps. A failed or cancelled one covers only the pages that landed, and its
  landed range is `[effective start, position + timeframe)`, empty when
  nothing landed.
- **Coverage is extended after the page is persisted, never before**, so it
  can never claim Bars a failed write did not store.
- **State is published before gap detection runs**, and `done` closes after
  both. `Wait(id)` is the only way to observe the finished Dataset; polling
  `Backfill(id)` can see the terminal state while gap detection is still in
  flight. `Wait` exists for tests and is not used by the HTTP layer.
- **One running Backfill per Dataset is enforced by scanning the registry**,
  not by a second index: the registry holds one process's Backfills, and a
  linear scan under the mutex is cheaper than keeping two maps agreeing.
  Entries are never evicted, which is the same `ponytail:` debt as the
  registry itself.
- **`DetectGaps` reads present open_times into a set** rather than merge-
  walking two sorted sequences. One month of `1m` is 44,640 entries; ticket 05
  owns the version that has to scale.
- **Gap detection over a landed range is indistinguishable from one over the
  effective range by the Store's contents** — Coverage already bounds it — so
  the test asserts it through the range `ReplaceOpenGaps` is called with, via
  a Store wrapper around the real DuckDB Store. No production test hook was
  needed.
- **`New` takes the last Provider when two share a name.** Duplicate names
  are a wiring bug, and the constructor returns no error.

## Ticket 05 (gap detection + Complete)

- **Contiguity is in the calendar's expected sequence**, not on the clock: a
  run of missing open_times ends only at an expected open_time that is present
  or settled. A market-closed period is therefore never a Gap *and* never
  breaks a run across it — Bars absent on both sides of a closed weekend are
  one Gap `[firstMissing, lastMissing+tf)` spanning it. A run also never spans
  two disjoint Coverage pieces: outside Coverage nothing is expected.
- `DetectGaps` marks **repaired** any Gap intersecting the range, in any
  status but `repaired`, whose range the Dataset now holds in full — `open`
  (a Gap that was filled), and also `ignored`/`unrecoverable` (ticket 06's
  Repair of a settled Gap whose data now exists). `SetGapStatus(id, repaired,
  "")` clears the operator's reason with the status it explained.
- A Gap range the calendar expects *nothing* in is **not** "fully present": a
  settled Gap sitting over a period the calendar later closed keeps its
  status instead of silently becoming `repaired`.
- A `repaired` Gap excludes nothing from expected; only `ignored` and
  `unrecoverable` Gaps that are still not filled do.
- Order of writes: read the Gaps intersecting the range, mark the filled ones
  repaired, compute the new open Gaps, then `ReplaceOpenGaps`. This is safe
  because the Store deletes only `status = 'open'` records (ticket 02), so a
  Gap repaired a moment earlier survives the replacement.
- `DetectGaps` returns the open Gaps the Store holds in the range *after* the
  replacement, so every returned Gap carries its database id — what
  `GET …/complete` and `GET …/gaps` need to name one.
- `IsComplete` returns the **open** Gaps intersecting the range (half-open
  overlap: a Gap that only touches the range is not listed and does not
  block). Settled and repaired Gaps neither block nor appear.
- An **empty range is trivially Complete** with no Gaps: it holds no instant
  that could be missing, and it is not judged against Coverage.

## Ticket 06 (Repair + gap status)

- **An empty reason is allowed for every settable status.** The spec says
  `PATCH /gaps/{id} {status, reason}` without saying the reason is mandatory,
  so `SetGapStatus` stores whatever it is given, empty included, for `open`,
  `ignored` and `unrecoverable` alike. Requiring one for `ignored` /
  `unrecoverable` is a policy the HTTP layer can add later without changing
  this use case.
- **`ErrGapStatusNotSettable` lives in `internal/app`, not `internal/domain`.**
  Which statuses an *operator* may assert is a use-case rule — the domain's
  `Gap` legitimately holds all four — so the sentinel sits next to the use
  case that enforces it, and HTTP maps it to 400 with `errors.Is`. It also
  covers a status outside the four canonical ones, so the use case rejects
  garbage before the Store sees it.
- **`SetGapStatus` delegates the existence check to the Store.** The Store's
  `SetGapStatus` already reports `domain.ErrNotFound` for an unknown Gap
  (ticket 02), so the use case does not load the Gap first — one round trip,
  same error.
- **`Repair` goes through `StartBackfill`, so it inherits everything a
  Backfill has**: the one-running-per-Dataset rule (`domain.ErrBackfillRunning`
  propagates unchanged), the in-memory registry entry, `Wait`, cancellation,
  and the terminal gap detection. Its returned range is therefore the
  *effective* range — the Gap's range with its start clipped up to the
  Provider's earliest available Bar. A Gap only ever lies inside Coverage, so
  in practice that clipping is a no-op and the range is exactly the Gap's.
- **`Repair` sets no status itself.** The Backfill's terminal `DetectGaps`
  runs over the landed range, which is the Gap's range, and ticket 05 already
  marks any non-repaired Gap `repaired` once its range is fully present — so
  an `ignored` or `unrecoverable` Gap whose data now exists is repaired by the
  same code path as an `open` one, with no special case.

## Ticket 07 (HTTP API adapter)

The spec fixes the routes, the statuses and the two headers; every field name
and every format below is a choice made here.

### Field names and formats

| Where | Document |
| --- | --- |
| every failure | `{"error":"message"}` — the only error shape, at every status |
| `GET /providers` | `[{"name":"binance","timeframes":["1m","1h",…]}]` |
| a range, anywhere | `{"start":"2024-01-01T00:00:00Z","end":"2024-01-02T00:00:00Z"}` |
| a Dataset, anywhere | `{"provider":"binance","symbol":"BTCUSDT","timeframe":"1m"}` |
| `POST /backfills` request | `{"provider","symbol","timeframe","start","end"}` |
| `POST /backfills` 202 | `{"id":"<32 hex>","effective_range":{"start","end"}}` |
| `GET /backfills/{id}` 200 | `{"id","dataset":{…},"range":{…},"state","bars_downloaded","position","last_error"}` |
| `DELETE /backfills/{id}` 202 | the same Backfill document |
| `GET …/coverage` 200 | `[{"start","end"}]` |
| `GET …/complete` 200 | `{"complete":true,"gaps":[…]}` |
| a Gap, anywhere | `{"id":1,"dataset":{…},"range":{…},"status":"open","reason":""}` |
| `PATCH /gaps/{id}` request | `{"status","reason"}` |
| `PATCH /gaps/{id}` 200 | the updated Gap document |
| `POST /gaps/{id}/repair` 202 | `{"backfill_id":"<32 hex>"}` |
| `GET …/bars?format=json` 200 | `[{"open_time","open","high","low","close","volume"}]` |

- **Instants are RFC3339 in UTC, seconds precision** (`2024-01-01T00:00:00Z`),
  in every field and in both directions. Bounds and open_times fall on
  Timeframe boundaries, so nothing is lost. `start` and `end` are *read* as
  RFC3339 **or** as a `YYYY-MM-DD` date at midnight UTC, because the
  acceptance criterion spells a backfill as `… 2024-01-01 2024-02-01`.
- **Prices and volume stay decimal strings**, exactly as `DECIMAL(20,8)`
  stores them (`"10.00000000"`), so no value passes through a float.
- **`bars_downloaded` is a number; `position` is null** until the first Bar
  lands — the zero instant is not a position — and `last_error` is `""`, not
  null, when there is none.
- **Empty lists are `[]`, never `null`**: Coverage, gaps, bars and the
  `X-Gaps` header alike.
- Ids: a Backfill id is the service's 32-hex string; a Gap id is the Store's
  integer, so `PATCH /gaps/abc` is a 400 before anything is looked up.

### Behaviour the spec leaves open

- **`DELETE /backfills/{id}` answers 202 with the Backfill document**, not
  204. Cancellation is a request, not an event: the run stops at its next
  page, so the body is the status as it stands (often still `running`) and the
  caller polls `GET /backfills/{id}` for the terminal state.
- **`Content-Type` of the default bars response is
  `application/vnd.apache.parquet`**; `?format=json` answers
  `application/json`. `?format=parquet` is accepted as the explicit spelling
  of the default, and any other value is a 400 — checked *before* the headers
  are set, so a rejected request carries no `X-Complete`.
- **`X-Complete` and `X-Gaps` are computed and set before the body starts**,
  for JSON and Parquet alike: `IsComplete` runs first, then the export streams
  straight to the `ResponseWriter`. A range is never refused for being
  incomplete — that is the whole point of the two headers.
- If the Parquet export fails **after** bytes are on the wire, the status is
  already 200 and cannot be taken back: the stream is truncated and the
  failure is logged. A failure before the first byte is still a normal JSON
  error response.
- **An empty or reversed range is a 400** on `POST /backfills`, `…/complete`
  and `…/bars`. `IsComplete` would call `[t,t)` trivially complete, which is a
  true answer to a question nobody meant to ask.
- **Only the Timeframe in a Dataset path is validated.** A Provider or Symbol
  the service never acquired is an empty Dataset — `[]` coverage, `[]` gaps,
  `complete:false` — not a 400: queries flag what is missing, they do not
  refuse. An unknown Provider is a 400 only where it must actually be used, on
  `POST /backfills`.
- **Status mapping**, the whole error contract: `domain.ErrNotFound` → 404,
  `domain.ErrBackfillRunning` → 409, `domain.ErrUnknownSymbol` /
  `domain.ErrUnsupportedTimeframe` / `app.ErrUnknownProvider` /
  `app.ErrGapStatusNotSettable` / malformed JSON / bad parameters → 400,
  anything else → 500 and a log line. The message is the error's own text.
- **The mux's own 404 and 405 are rewritten as JSON** by the one middleware
  this package has, which also logs a line per request. It rewrites only a
  404/405 that does not already carry the JSON content type, so a handler's
  own 404 passes through, and the 405 keeps the `Allow` header the mux set.
- **`(*app.Service).Bars` was added to the `Store` port** (the DuckDB adapter
  already had the method) together with thin `Coverage`, `Gaps`, `Gap`, `Bars`
  and `ExportParquet` pass-throughs on the Service, so the HTTP adapter
  depends on the use cases alone and never reaches the Store.

## Ticket 08 (CLI)

The spec names the commands and the two environment variables; every exit
code, every printed line and every default below is a choice made here.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | the command did what it says |
| 1 | the request was made and failed — any non-2xx, or a transport error |
| 2 | usage: a missing, unknown or extra argument, or an unknown flag |

- **A non-2xx prints `error: <message>` to stderr**, where the message is the
  `{"error": …}` document every route answers with, falling back to the HTTP
  status line (`400 Bad Request`) when the body is not one. Exit 1. Nothing is
  written to stdout, and `query` creates no file.
- **A usage error prints the whole usage text to stderr** and makes no
  request at all, which the tests assert.
- **`data backfill -wait` exits 1 on a `failed` Backfill** (`error: backfill
  <id> failed: <last_error>` on stderr) and 0 on `completed` or `cancelled`:
  cancelling is something the operator asked for, failing is not.

### Output formats

- **`providers`, `status`, `cancel`, `gaps`, `repair`** print the response
  body as indented JSON (two spaces) and nothing else.
- **`backfill`** prints two lines, then, with `-wait`, two more:

  ```
  id: 8f14e45fce7a4f0e9b2c9dd3ab5e7a71
  effective range: 2024-01-01T00:00:00Z .. 2024-02-01T00:00:00Z
  state: completed
  bars downloaded: 44640
  ```

- **`complete`** prints the verdict on the first line, then the whole
  document:

  ```
  complete: true
  {
    "complete": true,
    "gaps": []
  }
  ```

  It exits **0 whether the verdict is true or false** — it is a query, and a
  false answer is a successful one.
- **`query`** prints the `X-Complete` verdict to **stdout** (not stderr), as
  the first line, before any body byte, and names the gaps only when it is
  false:

  ```
  complete: false
  gaps: [{"id":1,"dataset":{…},"range":{…},"status":"open","reason":""}]
  wrote 4096 bytes to bars.parquet
  ```

  `-o -` writes the body to stdout instead of a file, after the verdict line
  and with no trailing `wrote …` line. A response with no `X-Complete` header
  prints `complete: unknown`.

### Behaviour

- **Flags follow the positional arguments** (`data gaps binance BTCUSDT 1m
  -status open`), which the spec's own spelling of the acceptance command
  implies. The standard `flag` package stops at the first non-flag argument,
  so the two groups are split before parsing; flags first still works.
- **`-o` is required for `query`** — a Parquet stream has no sensible default
  destination, so omitting it is a usage error rather than a dump to the
  terminal.
- **`-wait` polls `GET /backfills/{id}` every `-interval`, default 200ms**,
  with no overall deadline: a Backfill of a year takes as long as it takes and
  Ctrl-C is the way out.
- **Times are passed through verbatim.** The client never parses a bound; the
  service accepts RFC3339 or `YYYY-MM-DD` and is the only place that decides.
- **`AGNOFORGE_URL` defaults to `http://localhost:8080`**, which is where a
  `serve` with the default `AGNOFORGE_LISTEN` answers. The client's HTTP
  timeout is 10 minutes, because a Parquet export of a large range streams.
- **`serve` announces its bound address on stdout** (`listening on
  127.0.0.1:54321`) before the first request. That is what makes
  `AGNOFORGE_LISTEN=127.0.0.1:0` usable — by a test, and by anyone who wants
  the kernel to pick the port — and it is the only thing `serve` writes to
  stdout; the logs go to stderr.
- **`serve` shuts down gracefully on SIGINT and SIGTERM**
  (`signal.NotifyContext` + `Shutdown`, 10s grace), and the DuckDB database is
  closed only after the last request has finished.
- **The Provider's HTTP client has a 30s timeout per request**; retries,
  backoff and the rate-limit budget stay the adapter's business.
- **`BINANCE_BASE_URL` is read through `binance.BaseURL()`**, so the default
  (the public endpoint) is spelled in exactly one place — the adapter.
- **`cmd/agnoforge` is the only package that names a concrete adapter.** Two
  tests hold that line: one asserts `go list -deps ./cmd/agnoforge` reaches
  all three adapters, the other that no package in the module depends on
  `cmd/agnoforge`.

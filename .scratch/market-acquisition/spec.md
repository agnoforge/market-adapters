# Market Data Acquisition — Phase 1 spec

Vocabulary: `CONTEXT.md`. Decisions: `docs/adr/`. This spec is implementation-ready acceptance criteria for Phase 1 only.

## Shape

Headless Go service, hexagonal layout:

```
cmd/agnoforge                 serve + client subcommands
internal/domain               Dataset, Bar, Timeframe, Range, Gap, Coverage, invariants
internal/app                  Backfill, DetectGaps, Repair, Query, IsComplete use cases
internal/adapters/binance     Provider adapter (REST /api/v3/klines)
internal/adapters/duckdb      Store adapter
internal/adapters/httpapi     REST inbound adapter (net/http, Go 1.22 mux)
```

No scheduler/broker (ADR-0001). DuckDB via cgo (ADR-0002). No auth. Config via env (`AGNOFORGE_DB_PATH`, `AGNOFORGE_LISTEN`, `BINANCE_BASE_URL`).

## Ports

```go
type Provider interface {
    Name() string
    SupportedTimeframes() []Timeframe            // binance: 1m 3m 5m 15m 30m 1h 2h 4h 6h 8h 12h 1d 3d
    EarliestAvailable(ctx, Symbol) (time.Time, error) // ErrUnknownSymbol is permanent
    Calendar(Symbol) TradingCalendar             // binance: Continuous
    Bars(ctx, Symbol, Timeframe, Range) iter.Seq2[[]Bar, error] // adapter owns paging, rate limit, retry
}
type Store interface {
    UpsertBars(ctx, DatasetID, []Bar) error      // INSERT OR REPLACE on PK
    ExtendCoverage(ctx, DatasetID, Range) error  // union-merge
    Coverage(ctx, DatasetID) ([]Range, error)
    OpenTimes(ctx, DatasetID, Range) iter over open_time
    ReplaceOpenGaps(ctx, DatasetID, Range, []Gap) error // delete open gaps ∩ range, insert new
    Gaps(ctx, DatasetID, filter) ([]Gap, error)
    SetGapStatus(ctx, GapID, status) error
    ExportParquet(ctx, DatasetID, Range, w io.Writer) error
}
```

## Rules

- Range is half-open `[start,end)` on `open_time`; open_time is UTC epoch ms.
- Adapter clips `end` to the last fully closed bar and `start` to `EarliestAvailable`; response reports the effective range.
- Bars with `low > min(open,close)`, `high < max(open,close)`, or non-positive prices are dropped and logged.
- Prices/volumes stored as `DECIMAL(20,8)`.
- One running Backfill per Dataset; second request → 409. Different Datasets run in parallel sharing the adapter's token bucket (Binance: 6000 weight/min, klines weight 2).
- Backfill states: `running → completed | failed | cancelled`. Registry is in-memory (`ponytail:` persist when a Backfill must survive restart). Each yielded page = upsert + ExtendCoverage.
- On any terminal state, DetectGaps runs over the landed range: expected (from Calendar) − present − ranges of `ignored`/`unrecoverable` gaps; `open` gaps in range are replaced. A Repair whose range becomes fully present sets the gap `repaired`.
- `Complete(dataset, range)` = range ⊆ Coverage ∧ no `open` gap intersects.
- Query never refuses an incomplete range; it flags it.
- Retries in adapter: 5 attempts, exponential backoff, honour `Retry-After` on 429/418. Unknown symbol / unsupported timeframe are permanent → Backfill `failed` immediately.

## HTTP API

```
GET    /providers                                → [{name, timeframes}]
POST   /backfills   {provider,symbol,timeframe,start,end} → 202 {id, effective_range}
GET    /backfills/{id}                           → {state, bars_downloaded, position, last_error}
DELETE /backfills/{id}                           → cancel
GET    /datasets/{p}/{s}/{tf}/coverage           → [ranges]
GET    /datasets/{p}/{s}/{tf}/complete?start&end → {complete, gaps[]}
GET    /datasets/{p}/{s}/{tf}/gaps?status=       → [gap]
PATCH  /gaps/{id} {status, reason}               → open|ignored|unrecoverable
POST   /gaps/{id}/repair                         → 202 {backfill_id}
GET    /datasets/{p}/{s}/{tf}/bars?start&end[&format=json]
        default Parquet stream; headers X-Complete: true|false, X-Gaps: <json>
```

## CLI

`agnoforge serve`; `agnoforge data providers|backfill|status|cancel|gaps|repair|complete|query` — thin HTTP clients (`AGNOFORGE_URL`).

## Acceptance (Phase 1 done when)

1. `agnoforge data backfill binance BTCUSDT 1m 2024-01-01 2024-02-01` lands 44,640 bars; rerun changes nothing.
2. A range with a real Binance maintenance hole shows an `open` gap; `PATCH` to `ignored` makes `complete` return true.
3. `query` returns a Parquet file DuckDB can read; `X-Complete` is correct.
4. Adding a fake second Provider in tests requires no change under `internal/app`.

## Reserved for later (visible, not built)

- Binance bulk archive adapter (`data.binance.vision` monthly/daily zips) — same Provider name, yields one month per page.
- `1s`, `1w`, `1M` timeframes once TradingCalendar supports variable-length bars.
- Persisted Backfill registry; Revision records on conflicting re-download (needed by CSV import).

# agnoforge — Market Data Acquisition

Go service that downloads historical OHLCV bars from a provider (Binance, 1m and up), stores them exactly as returned in an embedded DuckDB file, tracks what was requested (**Coverage**) and what is missing (**Gaps**), and serves the bars to other AgnoForge services over HTTP as JSON or Parquet.

It never derives, aggregates, or merges bars. Vocabulary (Dataset, Backfill, Coverage, Gap, Complete, Repair) is defined in [`CONTEXT.md`](CONTEXT.md); design decisions are in [`docs/adr/`](docs/adr/).

## Layout

```
cmd/agnoforge/        CLI: `serve` runs the service, `data …` is an HTTP client for it
internal/domain/      Bar, Dataset, Timeframe, Range, Gap, Trading Calendar
internal/app/         use cases: Backfill, Gaps, Complete, Repair, Query
internal/adapters/
  binance/            Provider (REST klines)
  duckdb/             Store (bars, coverage, gaps, Parquet export)
  httpapi/            HTTP adapter
```

Dependency direction: `domain ← app ← adapters ← cmd`. Only external dependency: `github.com/duckdb/duckdb-go/v2`.

## Build & test

```sh
go build ./cmd/agnoforge
go test -race ./...
```

## Run the service

```sh
AGNOFORGE_DB_PATH=/tmp/agnoforge.duckdb ./agnoforge serve
```

| Env var            | Default             | Meaning                                  |
|--------------------|---------------------|------------------------------------------|
| `AGNOFORGE_LISTEN` | `:8080`             | listen address                           |
| `AGNOFORGE_DB_PATH`| `agnoforge.duckdb`  | DuckDB file (created if missing)         |
| `BINANCE_BASE_URL` | Binance public API  | override for tests / mirrors             |
| `AGNOFORGE_URL`    | `http://localhost:8080` | used by `data` commands to reach the service |

## Use the CLI

Times are RFC3339 (`2024-01-01T00:00:00Z`) or `YYYY-MM-DD`, UTC. Ranges are half-open `[start, end)`.

```sh
./agnoforge data providers
./agnoforge data backfill binance BTCUSDT 1m 2024-01-01 2024-02-01 -wait   # → 44,640 bars
./agnoforge data status   <backfill-id>
./agnoforge data cancel   <backfill-id>
./agnoforge data complete binance BTCUSDT 1m 2024-01-01 2024-02-01
./agnoforge data gaps     binance BTCUSDT 1m [-status open|repaired|ignored|unrecoverable]
./agnoforge data repair   <gap-id>
./agnoforge data query    binance BTCUSDT 1m 2024-01-01 2024-02-01 -o /tmp/jan.parquet   # or -o - -format json
```

Exit codes: `0` ok, `1` request failed, `2` usage error. `agnoforge help` prints full usage.

## HTTP API

```
GET    /providers
POST   /backfills                                    {provider,symbol,timeframe,start,end}
GET    /backfills/{id}
DELETE /backfills/{id}
GET    /datasets/{provider}/{symbol}/{timeframe}/coverage
GET    /datasets/{provider}/{symbol}/{timeframe}/complete?start=&end=
GET    /datasets/{provider}/{symbol}/{timeframe}/gaps[?status=]
GET    /datasets/{provider}/{symbol}/{timeframe}/bars?start=&end=[&format=parquet|json]   (default parquet; X-Gaps header lists open gaps)
PATCH  /gaps/{id}                                    {status: ignored|unrecoverable}
POST   /gaps/{id}/repair
```

Wire-format details and every spec-silent choice are recorded in [`ASSUMPTIONS.md`](ASSUMPTIONS.md). Current status and next steps: [`where-we-are-at.md`](where-we-are-at.md).

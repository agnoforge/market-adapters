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

## Playground

Run `agnoforge serve`, then open `http://localhost:8080/playground/`. There is nothing else to start: no build step, no tracing backend beside it.

Pick one of the ten operations, fill in the form — every parameter is marked path/query/body and required/optional, and comes prefilled with a working example — and press Execute. The page then shows:

- the HTTP request it is about to send, the equivalent `curl`, and the equivalent positional CLI command (`data backfill binance BTCUSDT 1m …`), each with a copy button;
- the response: status, duration, every header, and the body (JSON pretty-printed, Parquet reported as `binary, N bytes`);
- the trace behind that request, drawn as a waterfall coloured by layer — `httpapi → app → provider/store` — where clicking a span opens its ids, times, status, attributes, events and links, and the first Error span is outlined and opened for you.

Start a Backfill answers `202` and offers an **Execution trace** button. The worker runs after the response, in a trace of its own linked back to the request span, so the button polls that trace by `backfill_id` until no span is still running.

Every domain response carries an `X-Trace-ID` header, and the CLI prints `trace: <id>` beneath `error:` on any failure — paste that id into `/playground/traces/{id}` to see where it went wrong.

Traces are kept in the process: the last 256, in memory, gone when `serve` stops. Why there is no OTLP exporter and no Jaeger to run: [`docs/adr/0003-opentelemetry-in-process-trace-store.md`](docs/adr/0003-opentelemetry-in-process-trace-store.md).

Wire-format details and every spec-silent choice are recorded in [`ASSUMPTIONS.md`](ASSUMPTIONS.md). Current status and next steps: [`where-we-are-at.md`](where-we-are-at.md).

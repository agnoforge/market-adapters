# agnoforge — Market Data Acquisition

Go service that downloads historical OHLCV bars from a provider (Binance, 1m and up), stores them exactly as returned in an embedded DuckDB file, tracks what was requested (**Coverage**) and what is missing (**Gaps**), and serves the bars to other AgnoForge services over HTTP as JSON or Parquet.

Acquisition itself never derives, aggregates, or merges bars. A second bounded context in the same process — **Composite Market Datasets** — declares a research dataset over those source bars: one instrument, a base source, an optional cross-provider catch-up source, and the higher timeframes to derive. A **Build** reconciles the declaration against the data that really exists and records how fit the result is.

Acquisition's vocabulary (Dataset, Backfill, Coverage, Gap, Complete, Repair) is defined in [`internal/acquisition/CONTEXT.md`](internal/acquisition/CONTEXT.md) and the composite one (Composite Dataset, Instrument, Segment, Transition, Quality, Build) in [`internal/composite/CONTEXT.md`](internal/composite/CONTEXT.md); how the two relate is [`CONTEXT-MAP.md`](CONTEXT-MAP.md). Design decisions are in [`docs/adr/`](docs/adr/). Visual quick-start: [`README.html`](README.html).

## Layout

```
cmd/agnoforge/        CLI: `serve` runs the service, `data …` and `composite …` are HTTP clients for it
internal/acquisition/domain/      Bar, Dataset, Timeframe, Range, Gap, Trading Calendar
internal/acquisition/app/         use cases: Backfill, Gaps, Complete, Repair, Query
internal/acquisition/adapters/
  binance/            Provider (REST klines)
  duckdb/             Store (bars, coverage, gaps, Parquet export)
  httpapi/            HTTP adapter
  playground/         developer playground: operation catalog, trace store, UI
internal/composite/   Composite Market Datasets, its own context in the same process
  domain/             Composite Dataset, Segment, Transition, Quality, calendar timeframes
  app/                use cases: Create/Edit/Delete, Build (six phases), Bars
  adapters/duckdb/    Store (declarations, segments, quality, materialized bars)
  adapters/httpapi/   HTTP adapter for /composites
  adapters/acqport/   the acquisition control plane, in-process
```

Dependency direction: `domain ← app ← adapters ← cmd`, once per context. Only external dependency: `github.com/duckdb/duckdb-go/v2`.

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
| `AGNOFORGE_URL`    | `http://localhost:8080` | used by `data` and `composite` commands to reach the service |

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

Composite datasets are declared once and built on demand. A declaration is `-instrument <market> -base <provider:symbol> -start <t> -end <t|now>` plus the optional `-catch-up none|provider:symbol`, `-timeframes 5m,1h,1d` and `-mode strict|research`; an edit replaces the whole declaration.

```sh
./agnoforge composite create  btc-usd -instrument BTC/USD -base binance:BTCUSDT \
    -start 2024-01-01 -end now -timeframes 5m,1h,1d -mode research
./agnoforge composite list
./agnoforge composite get     btc-usd
./agnoforge composite edit    btc-usd -instrument BTC/USD -base binance:BTCUSDT -start 2024-01-01 -end now
./agnoforge composite build   btc-usd     # backfills what is missing, assembles, judges, materializes
./agnoforge composite quality btc-usd     # coverage %, open gaps, transitions, incomplete windows
./agnoforge composite query   btc-usd -o /tmp/btc.parquet [-timeframe 1h] [-start t] [-end t] [-format json]
./agnoforge composite delete  btc-usd
```

`build` is the whole reconciliation: it resolves `now` to the last closed 1-minute bar, backfills whatever the base source is missing, asks a configured catch-up provider for the tail alone, validates the provider boundary, repairs the gaps it finds, records the price movement across every Transition, and derives the declared timeframes. A **strict** dataset refuses to be ready with an open gap or an incomplete window; a **research** one records them and stays usable. `query` omits nothing quietly: the state, mode, timeframe and range come back as `X-Composite-State`, `X-Composite-Mode`, `X-Timeframe` and `X-Range-Start`/`X-Range-End`.

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

The composite context serves its own resource on the same port:

```
POST   /composites                                   {name,instrument,base:{provider,symbol,timeframe},catch_up,requested_start,requested_end,timeframes,mode}
GET    /composites
GET    /composites/{name}                            declaration + segments (provenance) + quality
PUT    /composites/{name}                            replaces the whole declaration
DELETE /composites/{name}
POST   /composites/{name}/build                      runs a Build; 409 while one is already running
GET    /composites/{name}/bars[?timeframe=&start=&end=&format=parquet|json]   (default parquet, default the dataset's own resolved range)
GET    /composites/{name}/quality                    the last Build's verdict, with the state and mode it belongs to
```

## Playground

Run `agnoforge serve`, then open `http://localhost:8080/playground/`. There is nothing else to start: no build step, no tracing backend beside it.

Pick one of the operations — the ten acquisition routes, then the composite ones — fill in the form (every parameter is marked path/query/body and required/optional, and comes prefilled with a working example) and press Execute. The page then shows:

- the HTTP request it is about to send, the equivalent `curl`, and the equivalent positional CLI command (`data backfill binance BTCUSDT 1m …`, `composite build btc-usd`), each with a copy button;
- the response: status, duration, every header, and the body (JSON pretty-printed, Parquet reported as `binary, N bytes`);
- the trace behind that request, drawn as a waterfall coloured by layer — `httpapi → app → provider/store`, and `composite-app` for the composite context — where clicking a span opens its ids, times, status, attributes, events and links, and the first Error span is outlined and opened for you.

Start a Backfill answers `202` and offers an **Execution trace** button. The worker runs after the response, in a trace of its own linked back to the request span, so the button polls that trace by `backfill_id` until no span is still running.

Build a Composite Dataset runs inside the request, so its whole trace is the response's own: `composite.Build` with one child span per phase — `resolve`, `ensure`, `assemble`, `validate`, `quality`, `materialize` — each carrying the dataset name and the resolved range, and each recording its own failure. A build that broke is the phase whose bar is red, and the acquisition spans it drove sit underneath it in the same waterfall.

The two declaration routes (`POST /composites`, `PUT /composites/{name}`) are not in the catalog: their body is a nested document, and every form the playground builds is flat. Declare a dataset with `agnoforge composite create` and drive the rest from the page.

Every domain response carries an `X-Trace-ID` header — the composites resource included — and the CLI prints `trace: <id>` beneath `error:` on any failure; paste that id into `/playground/traces/{id}` to see where it went wrong.

Traces are kept in the process: the last 256, in memory, gone when `serve` stops. Why there is no OTLP exporter and no Jaeger to run: [`docs/adr/0003-opentelemetry-in-process-trace-store.md`](docs/adr/0003-opentelemetry-in-process-trace-store.md).

Wire-format details and every spec-silent choice are recorded in [`ASSUMPTIONS.md`](ASSUMPTIONS.md). Current status and next steps: [`where-we-are-at.md`](where-we-are-at.md).

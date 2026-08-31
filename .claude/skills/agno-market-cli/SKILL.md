---
name: agno-market-cli
description: "Get price bars (OHLCV) from Binance with the agnoforge CLI, check what is already stored, and manage composite datasets (declared, aggregated, versioned bar series). Use when a task needs candles for a symbol and timeframe, asks to download history, asks what data we have, or mentions agnoforge, backfills, coverage, gaps, composites, aggregation or derived timeframes. Trigger: /agno-market-cli."
---

# agno-market-cli

Run from `/Volumes/XPG/Projects/agnos/market-acquisition`. Build with `go build ./cmd/agnoforge`.

Every `data` command is an HTTP client for a running `serve`, so start the service first:

```sh
curl -sf -m 2 http://localhost:8080/providers >/dev/null || ./agnoforge serve &
```

Read stored bars with `data query`. While `serve` runs it holds a lock on
`agnoforge.duckdb`, so a second DuckDB process cannot open the file.

## Commands

`agnoforge help` lists them all. The ones you will use:

```sh
./agnoforge data providers                                   # providers + timeframes
./agnoforge data complete binance BTCUSDT 1m 2024-01-01 2024-02-01
./agnoforge data backfill binance BTCUSDT 1m 2024-01-01 2024-02-01 -wait
./agnoforge data status   <backfill-id>                      # running|completed|failed|cancelled
./agnoforge data gaps     binance BTCUSDT 1m [-status open|repaired|ignored|unrecoverable]
./agnoforge data repair   <gap-id>
./agnoforge data query    binance BTCUSDT 1m 2024-01-01 2024-02-01 -o /tmp/jan.parquet
```

Times are `YYYY-MM-DD` or RFC3339, UTC, and ranges are half-open `[start, end)`.
Timeframes run `1m` to `3d` (`data providers` lists them).

Exit codes: `0` ok, `1` request failed, `2` usage error. A failure prints `error: …` and
`trace: <id>`; open `http://localhost:8080/playground/traces/<id>` to see which layer broke.

## Getting data you don't have yet

1. `data complete` — first line reads `complete: true` or `complete: false`.
2. On false, `data backfill … -wait` over the missing range. `-wait` polls until it
   finishes; without it you get an id back and poll with `data status`.
3. `data gaps` then shows what Binance itself does not have. `data repair <gap-id>`
   retries one; `PATCH /gaps/{id}` marks it `ignored` or `unrecoverable`.

Re-running a backfill over stored bars is safe (upsert), just slow — a month of `1m`
bars is ~44,640 rows and takes tens of seconds.

## Reading the output

`complete` and `query` print a human line before the payload, so skip it before parsing:

```sh
./agnoforge data query binance BTCUSDT 1m 2024-01-01 2024-01-02 -o - -format json | tail -n +2 | jq .
```

`query` adds a `gaps: …` line too when the range is incomplete, so for JSON prefer
`-o /tmp/bars.json -format json` and read the file. Default format is Parquet — use it
for anything large.

Bar fields: `open_time`, `open`, `high`, `low`, `close`, `volume`, all decimal strings
exactly as Binance returned them. The `data` side stores bars and never derives them, so
ask for the timeframe you want rather than aggregating a smaller one — aggregation is
what composites are for.

## Composite datasets

A composite is a named, declared bar series: a 1m base source, an optional catch-up
source, a requested range, and the timeframes to materialize by aggregation. Same
client conventions as `data` (running `serve`, exit codes, `trace:` on failure).

```sh
./agnoforge composite create btc-main -instrument BTC/USD -base binance:BTCUSDT \
    -start 2024-01-01 -end now -timeframes 5m,1h,1d
./agnoforge composite build   btc-main            # reconcile against stored source bars
./agnoforge composite query   btc-main -o /tmp/btc-1h.parquet -timeframe 1h
./agnoforge composite quality btc-main            # coverage/quality from the last build
./agnoforge composite list / get / edit / delete
```

The flow is declare → `build` → `query`. `build` reconciles the declaration against
the source bars already stored (backfill those first with `data backfill`) and fails
loudly when it cannot leave the dataset `ready`. States: `draft`, `building`, `ready`,
`stale`, `failed`; a source or declaration change makes it `stale` — rebuild. Modes:
`strict` (default) or `research` (`-mode`).

Flag notes: `-catch-up` is `none`, empty (base provider continues), or another
`provider:symbol` to continue past the base. `edit` takes the same flags as `create`
and replaces the whole declaration, not a patch. `query` prints `state:`, `mode:`,
`timeframe:` and `range:` lines before the payload (skip 4 lines when piping); both
bounds are optional and default to the dataset's own resolved range; omitting
`-timeframe` reads the 1m composite timeline.

## Environment

`AGNOFORGE_LISTEN` (`:8080`), `AGNOFORGE_DB_PATH` (`agnoforge.duckdb`), `AGNOFORGE_URL`
(what `data` dials), `BINANCE_BASE_URL` (defaults to `https://data-api.binance.vision`,
because `api.binance.com` answers 403 in many regions).

The same operations are available over HTTP (`README.md` § HTTP API) and in the browser
at `http://localhost:8080/playground/`.

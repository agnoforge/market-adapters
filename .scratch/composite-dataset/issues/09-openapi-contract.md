# 09 — OpenAPI contract for `/composites*`

**What to build:** An OpenAPI 3.1 document at `contracts/composite.openapi.yaml` (the path the backtesting requirements name in §16) that spells the whole consumer boundary of the composites resource — route list, request parameters, every JSON shape, the response-header contract, the Parquet column schema and the status/error mapping — exactly as implemented today, so that `core-engine`'s `CompositeHttpAdapter` can stop pinning a commit of this repo and target a contract instead. Nothing on the wire changes in this ticket; the document records what commit `3db181e` already does. Filed from `core-engine` (`.scratch/composite-integration/spec.md`, user story 33).

**Blocked by:** None — can start immediately. Ticket 10 (Parquet error signalling) may add a `Content-Length` line to the bars response; either order works, the later one amends the contract.

**Status:** ready-for-agent

- [ ] `contracts/composite.openapi.yaml` exists, is OpenAPI 3.1, and lints clean (any standard linter run in CI, or a `go test` that parses it)
- [ ] All eight routes below are present with the request parameters, status codes and header contract recorded in this ticket; the mux-level 404/405 JSON bodies and `X-Trace-ID` are documented once as resource-wide behaviour
- [ ] Every JSON document in `internal/composite/adapters/httpapi/wire.go` is a named component schema (`Composite`, `CompositeDetail`, `Config`, `Source`, `CatchUp`, `Segment`, `Quality`, `Gap`, `Transition`, `IncompleteWindow`, `Bar`, `QualityDocument`, `Error`), with the `omitempty` fields marked optional and the never-null arrays marked required
- [ ] The Parquet body of `GET /composites/{name}/bars` is documented as `application/vnd.apache.parquet` with the six-column schema (column names, types, order, unit of `open_time`)
- [ ] The nine Timeframe tokens, five states and two modes are closed enums in the document; `1M` and `1m` are noted as case-sensitive
- [ ] A test keeps the contract honest: each recorded example response in the harness tests decodes against its schema, or at minimum the route/method list in the document is asserted equal to the patterns registered in `api.go`
- [ ] README's API table links the contract; `where-we-are-at.md` updated

## What the consumer relies on

`core-engine` (the private Rust backtester) implements a `HistoricalDataPort` over this resource with a `CompositeHttpAdapter` (its spec: `core-engine/.scratch/composite-integration/spec.md` § "CompositeHttpAdapter"; its ticket: `core-engine/.scratch/composite-integration/issues/07-composite-http-adapter.md`). It uses exactly this subset, and the contract must keep every line of it stable:

- `describe` = `GET /composites/{name}`: reads `instrument`, `timeframes` (adds the implicit `1m` itself), `state`, `mode`, `resolved_end` and the embedded `quality` document (`coverage_percentage`, `open_gap_count`, `transition_count`, `incomplete_window_count`, `materialization_version`, `last_build_at`, `available_start`, `available_end`). Maps 404 → "dataset not found", 409 and 400 → validation errors carrying the `error` text verbatim, anything else → "Composite returned N"/unreachable.
- `bars` = `GET /composites/{name}/bars?timeframe=…&start=…&end=…` with the **default Parquet** body (`?format` is never sent). `1m` ranges are split into calendar-month half-open windows spelled as RFC3339 UTC; every other frame is one request; `1w`/`1M` tokens are sent verbatim.
- Reads `X-Composite-State` and `X-Composite-Mode` on every bars response and fails the run if they differ from what `describe` said.
- Decodes the Parquet body fully and validates the footer/magic; a truncated body is an error (see ticket 10).
- Never uses `?format=json`, `POST`, `PUT`, `DELETE` or `/build` from the engine (its contract test drives create → build → describe → bars, so those routes must be in the document too).

## Routes as implemented at `3db181e`

Registered in `internal/composite/adapters/httpapi/api.go:38-45`, mounted at `/composites` and `/composites/` by `cmd/agnoforge/serve.go:154-157`, wrapped by `instrument()` (`cmd/agnoforge/serve.go:158`, `cmd/agnoforge/tracing.go:62-81`).

| Method & path | Handler | Success | Body |
|---|---|---|---|
| `POST /composites` | `composites.go:14` | `201` | `Composite` |
| `GET /composites` | `composites.go:40` | `200` | `[Composite]` (empty list is `[]`) |
| `GET /composites/{name}` | `composites.go:55` | `200` | `CompositeDetail` |
| `PUT /composites/{name}` | `composites.go:95` | `200` | `Composite` (whole declaration replaced; a built dataset comes back `stale`) |
| `DELETE /composites/{name}` | `composites.go:122` | `200` | `Composite` (the declaration as it stood) |
| `POST /composites/{name}/build` | `composites.go:78` | `200` | `CompositeDetail` (synchronous; returns when the Build is done) |
| `GET /composites/{name}/bars` | `bars.go:33` | `200` | Parquet stream (default) or `[Bar]` (`?format=json`) |
| `GET /composites/{name}/quality` | `bars.go:94` | `200` | `QualityDocument` (`quality` is `null` before any Build) |

Resource-wide:

- Unknown path under `/composites/` → `404 {"error":"not found"}`; known path, wrong method → `405 {"error":"method not allowed"}` — the mux's plain-text bodies are rewritten to JSON by `recorder.WriteHeader` (`api.go:63-96`).
- `X-Trace-ID` on every response, set before the handler runs (`cmd/agnoforge/tracing.go:26`, `:71-81`).
- `{name}` must be a kebab-case slug (`domain.ParseName`, `internal/composite/domain/name.go:17-19`); anything else is `400` before the store is consulted (`api.go:180-184`).
- Every instant on the wire is RFC3339 UTC (`wire.go:16-17`, `asTime` at `wire.go:186`); `requested_end` may also be the literal `"now"` (`wire.go:469-479`).

### `GET /composites/{name}/bars` — request

Parsed in `bars.go:117-161`:

| Param | Default | Accepted | Refused as |
|---|---|---|---|
| `timeframe` | `1m` (`bars.go:135`) | one of `1m 5m 15m 30m 1h 4h 1d 1w 1M` (`internal/composite/domain/timeframe.go:23-31`), case-sensitive | `400` — `ErrInvalidConfig: unknown timeframe "x"` (`timeframe.go:59-62`) |
| `start` | dataset's `requested_start` (`internal/composite/domain/query.go:68-70`) | RFC3339 instant or `YYYY-MM-DD` read as 00:00 UTC (`wire.go:455-467`) | `400` — `ErrInvalidConfig` |
| `end` | dataset's `resolved_end` (`query.go:71-73`) | same | `400`; no resolved end to default to → `409 ErrNotBuilt` (`query.go:74-76`) |
| `format` | `parquet` (`bars.go:117-127`) | `parquet` or `json` | `400` — `ErrInvalidConfig: format "x" is neither …` |

Range is half-open `[start, end)`; an empty range is `400` (`query.go:77-80`). Bars come back ascending by `open_time` (`internal/composite/adapters/duckdb/query.go:41`, `:88`).

### `GET /composites/{name}/bars` — response headers

Set after `PlanBars` succeeded and before any byte of the body (`bars.go:55-59`), so they are present on every `200` and absent from every planning error:

| Header | Value | Source |
|---|---|---|
| `X-Composite-State` | `draft`, `building`, `ready`, `failed`, `stale` (`internal/composite/domain/dataset.go:47-57`; in practice only `ready`/`stale` can reach a bars `200`, the rest fail planning) | `bars.go:55` |
| `X-Composite-Mode` | `strict` or `research` (`dataset.go:19-22`) | `bars.go:56` |
| `X-Timeframe` | the timeframe served | `bars.go:57` |
| `X-Range-Start`, `X-Range-End` | the resolved half-open bounds, RFC3339 UTC | `bars.go:58-59` |
| `Content-Type` | `application/vnd.apache.parquet` (`api.go:113`, `bars.go:71`) or `application/json` (`api.go:124`) | |
| `X-Trace-ID` | see above | |

Note for the document: on the JSON path the five `X-*` headers are set *before* `svc.Bars` runs (`bars.go:55-67`), so a JSON-path `500` also carries them; on the Parquet path a pre-byte failure goes through `fail()` with the headers already set on the map (`bars.go:74-78`). Document them as "present on 200; may be present on a 5xx".

### Parquet body

Written by DuckDB `COPY … TO (FORMAT PARQUET)` into a temp file and then streamed (`internal/composite/adapters/duckdb/query.go:72-104`; layout also recorded in `ASSUMPTIONS.md:54-62`). Six columns, in order:

| Column | Parquet/DuckDB type | Meaning |
|---|---|---|
| `open_time` | `BIGINT` | UTC epoch milliseconds of the bar's open |
| `open`, `high`, `low`, `close`, `volume` | `DECIMAL(20,8)` | exact decimals as stored; never floats |

Dataset name, instrument and timeframe are **not** columns (`query.go:69-71`); the consumer stamps them from the request. A range with no bars is a valid empty Parquet file with the same schema (`query.go:164-170`). No `Content-Length` is sent today (the body is chunked) — see ticket 10.

### JSON bars body (`?format=json`)

`[Bar]`, `Bar = {open_time: RFC3339 UTC, open, high, low, close, volume: decimal strings}` (`wire.go:160-167`, `:189-202`); empty is `[]`, never `null`. Same spelling as acquisition's bars route by design.

### JSON shapes (all in `wire.go`)

| Component | Struct | Lines | Notes |
|---|---|---|---|
| `Error` | `errorJSON` | 20-22 | `{"error": string}`, body of every non-2xx |
| `Source` | `sourceJSON` | 27-32 | `instrument` (may be omitted on input = dataset's own), `provider`, `symbol`, `timeframe` (input default `1m`, `wire.go:414-421`) |
| `CatchUp` | `catchUpJSON` | 37-43 | `kind` ∈ `base` (default when omitted), `none`, `source`; the four source fields only when `kind=source` |
| `Config` | `configJSON` | 48-56 | `instrument`, `base`, `catch_up`, `requested_start`, `requested_end` (instant or `"now"`), `timeframes[]`, `mode` (default `strict`) |
| create request | `createRequestJSON` | 60-63 | `Config` + `name` |
| `Composite` | `compositeJSON` | 69-77 | `name` + `Config` + `state`, `resolved_end?`, `last_error?`, `created_at`, `updated_at` |
| `CompositeDetail` | `compositeDetailJSON` | 82-86 | `Composite` + `segments[]` (never null) + `quality` (nullable) |
| `Segment` | `segmentJSON` | 90-95 | `kind`, `source`, `start`, `end` (half-open) |
| `Gap` | `gapJSON` | 99-103 | `id` (acquisition's), `start`, `end` |
| `Transition` | `transitionJSON` | 110-117 | `at`, `from`, `to`, `close`, `open`, `price_delta` (decimal strings, empty when unpriced) |
| `IncompleteWindow` | `incompleteWindowJSON` | 123-127 | `timeframe`, `start`, `end` |
| `Quality` | `qualityJSON` | 133-154 | `requested_start`, `requested_end`, `resolved_end`, `available_start?`, `available_end?`, `expected_bars`, `actual_bars`, `coverage_percentage` (number), `open_gap_count`, `open_gaps[]`, `transition_count`, `transitions[]`, `incomplete_window_count`, `incomplete_windows[]`, `mode`, `strict` (bool), `materialization_version` (int), `last_build_at` |
| `QualityDocument` | `qualityDocumentJSON` | 176-183 | `name`, `state`, `mode`, `resolved_end?`, `last_error?`, `quality` (nullable) |

### Status / error mapping

`statusFor` in `api.go:150-170` is the whole mapping; sentinels in `internal/composite/domain/errors.go`. The body is always `{"error": "<sentinel text>: <detail>"}` (`api.go:130-135`) and the consumer preserves that text verbatim.

| Status | Sentinel(s) | When (bars route unless noted) |
|---|---|---|
| `400` | `ErrInvalidConfig` (`errors.go:13`) | non-slug name; unknown timeframe; malformed `start`/`end`; empty range; bad `format`; malformed declaration body / invalid JSON (create, edit — `api.go:173-178`) |
| `400` | `ErrTimeframeNotMaterialized` (`errors.go:49`) | a real timeframe this dataset does not serve; message names the frames it does (`domain/query.go:42-43`) |
| `404` | `ErrNotFound` (`errors.go:20`) | unknown dataset on any `{name}` route |
| `409` | `ErrNotBuilt` (`errors.go:43`) | dataset has no Segments — draft, or last Build could not be ready (`app/query.go:88-91`); or no `resolved_end` to default to (`domain/query.go:74-76`) |
| `409` | `ErrBuildRunning` (`errors.go:24`) | `POST …/build` while one is running (in-memory guard) |
| `409` | `ErrNotReady` (`errors.go:37`), `ErrTransitionInvalid` (`errors.go:31`) | `POST …/build` ran but left the dataset `failed`; the body names why and `GET` shows the Quality |
| `409` | `ErrDuplicateName` (`errors.go:17`) | `POST /composites` with a taken name |
| `500` | anything else | store/DuckDB failure, encoding failure; logged (`api.go:140-146`) |

A `stale` or `research` dataset is **served**, not refused; the state/mode is on the headers and on `GET`/`/quality` (`bars.go:29-32`, tests `bars_test.go:412-490`).

## Open questions for the document (do not invent answers in the YAML)

- **Compatibility/version information** (backtesting requirements §15) — there is no version endpoint or header today. Record the contract's own `info.version` and leave an explicit "no runtime version signal" note; a follow-up can add one.
- Whether `POST …/build` should stay synchronous in the contract or be marked as such with a warning that it can take minutes — document the current behaviour and its default client timeout in the CLI (10 minutes, `ASSUMPTIONS.md:401`).

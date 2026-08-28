---
status: accepted
---
# OpenTelemetry for tracing, with an in-process trace store as the only default sink

The Developer Playground needs to show, for one HTTP call, the path `httpapi → app use case → provider/store adapter → Binance/DuckDB` with durations and the failing step. ADR 0001 keeps this repo at one direct dependency, and the OpenTelemetry API+SDK add roughly fifteen modules. We take that cost anyway: OTel is itself one of the practices this project exists to showcase, and its `SpanProcessor`/`SpanExporter` seams give backend replaceability without writing a port of our own. `app` and the adapters import only the vendor-neutral `otel` API; the SDK is wired in `cmd`.

Traces are sunk into a bounded in-process store (last 256 traces) served from `/playground/traces/…` in the same process. No OTLP exporter dependency and no Jaeger/Tempo requirement: the local-first rule says `agnoforge serve` must be the whole setup. A remote backend is an exporter swap in `cmd` when someone needs one.

A Backfill's worker runs after the request returns, so it starts a **new** trace carrying a span Link to the request span and an `agnoforge.backfill.id` attribute; the store is queryable by that attribute. One minutes-long trace hanging off a 30 ms request was the alternative and misrepresents both.

## Considered options

- Hand-rolled ~150-line tracer, same span shape (rejected: reimplements the seam OTel already ships, and OTel is a showcase goal)
- OTLP export to a local Jaeger container (rejected: violates zero-setup local-first; kept as a later exporter swap)
- Worker spans as children of the request trace (rejected: one trace open for minutes, request duration meaningless)
- Decorator wrappers around `Store`/`Provider` in `cmd` instead of inline spans (rejected: cannot see page counts, gap counts or retries)

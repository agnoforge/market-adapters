# Goal run — Developer Playground + OpenTelemetry tracing

Prompt: `.scratch/playground/goal-prompt.md`. Spec: `.scratch/playground/spec.md`. ADR 0003.

## Outcomes
- [ ] O1 `01-otel-foundation.md`
- [ ] O2 `02-app-and-adapter-spans.md`
- [ ] O3 `03-trace-store-and-query.md`
- [ ] O4 `04-operation-catalog.md`
- [ ] O5 `05-playground-ui.md`
- [ ] O6 `06-docs-and-status.md`

## After-every-ticket invariants
1. `go build ./...`, `go vet ./...`, `go test -race ./...` clean.
2. Direct deps: duckdb-go, otel, otel/sdk, otel/trace, otelhttp only (+ tidy indirects). No OTLP/Jaeger/framework/JS toolchain.
3. `internal/domain` stdlib-only; `internal/app` imports `otel` API at most, never `sdk`; nothing under `internal/` imports `cmd`.
4. Eight domain routes + wire format unchanged; existing `httpapi` and `cmd` test assertions untouched.

## Budget
- Start: 1787928146 (2026-08-28 14:42 UTC). Deadline: 1787946146 (+5h).
- Per ticket: one sub-agent attempt + one correction round, then inline takeover.
- Pinned: go1.25.6; `go.opentelemetry.io/otel` v1.46.0, `otel/sdk` v1.46.0, `otel/trace` v1.46.0, `otelhttp` v0.71.0.

## Attempt log

### Ticket 01 — sub-agent pass, orchestrator verified (elapsed ~10m)
Sub-agent report: all 5 PASS. Tests `cmd/agnoforge/tracing_test.go` (X-Trace-ID==span trace id at 200/404/500; traceparent continued, parent span id checked; CLI `trace:` line; `go list` guard that `internal/` never reaches `otel/sdk` or `contrib`). Files: `cmd/agnoforge/{tracing.go,tracing_test.go,serve.go,data.go,main.go}`, go.mod/go.sum, ASSUMPTIONS §09.
Orchestrator re-verify: build/vet/`go test -race ./...` ok; `go list -deps ./internal/...` → 0 otel/sdk, 0 otel at all, 0 cmd; go.mod direct deps exactly duckdb + 4 otel; live serve on :0 → `X-Trace-Id: afcb24…` (32 hex), traceparent `4bf92f…` continued, `data status nope` → stderr `error: … / trace: 5b4c1c…` exit 1. Note: sub-agent's SDK guard test must exempt `internal/adapters/playground` in ticket 03.

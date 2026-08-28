Status: todo

# OpenTelemetry foundation: SDK wiring, HTTP middleware, X-Trace-ID

Add `go.opentelemetry.io/otel`, `sdk/trace`, `contrib/instrumentation/net/http/otelhttp`. `cmd/agnoforge serve` builds a `TracerProvider` (always-on sampler) and sets it globally; `httpapi` handler is wrapped with `otelhttp` and a tiny middleware writes `X-Trace-ID`. CLI prints `trace: <id>` on non-2xx. Write ADR 0003 (already drafted in `docs/adr/`).

## Done when
- [ ] `go build ./...`, `go vet ./...`, `go test -race ./...` green
- [ ] Only `cmd` imports `go.opentelemetry.io/otel/sdk/...`; `internal/app` and `internal/domain` import at most the `otel` API (`go list -deps` check in a test or `grep`)
- [ ] Every HTTP response from the server carries `X-Trace-ID` (32 hex), equal to the server span's trace id
- [ ] An inbound `traceparent` header is continued (response `X-Trace-ID` equals the inbound trace id)
- [ ] `agnoforge data …` on a non-2xx prints `trace: <id>` to stderr

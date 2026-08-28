Status: todo
Blocked by: 02

# In-process trace store and query endpoints

`internal/adapters/playground`: `SpanProcessor` (OnStart records running spans, OnEnd finalises), ring buffer of last 256 traces evicted oldest-first, index by trace id and by `agnoforge.backfill.id`. Handlers `GET /playground/traces/{id}` and `GET /playground/traces?backfill_id=` with the JSON shape in spec. Registered by `serve` on the same mux.

## Done when
- [ ] `GET /playground/traces/{id}` returns the trace for a just-made request's `X-Trace-ID`; unknown id → 404
- [ ] While a backfill is running, `?backfill_id=` returns a trace with `end: null` spans; after `Wait` all spans have `end`
- [ ] Filling 257 traces evicts the first; the 257th is retrievable
- [ ] Store is safe under `-race` with concurrent OnStart/OnEnd and reads
- [ ] `// ponytail:` comment names the per-trace cap as the upgrade path

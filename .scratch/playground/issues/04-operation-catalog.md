Status: todo
Blocked by: 01

# Operation catalog with curl and CLI templates

Hand-written `[]Operation` in `internal/adapters/playground` covering the ten domain routes: name, method, path, params (name, in: path|query|body, required, example), and a CLI template using the CLI's actual positional syntax. Served at `GET /playground/operations`.

## Done when
- [ ] Ten operations, one per route in `httpapi.api.go`
- [ ] Test: for every operation, substituting examples into method+path and hitting the real mux never yields 404 or 405
- [ ] Every operation that has a CLI equivalent (`agnoforge data …`) carries a template; ones without (e.g. list providers if absent) carry `null`
- [ ] Catalog JSON is served with `Content-Type: application/json`

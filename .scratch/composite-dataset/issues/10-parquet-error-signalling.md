# 10 — Parquet mid-stream error signalling

**What to build:** A way for a consumer of `GET /composites/{name}/bars` (Parquet) to *know* that the body it received is not the whole file. Today a failure after the first byte is on the wire leaves the status at `200`, the handler returns normally, Go's `net/http` writes the terminating chunk, and the client reads a clean, well-formed HTTP response whose payload is a truncated Parquet file — nothing at the HTTP layer says so. `core-engine` copes by decoding the whole body and validating the Parquet footer/magic, but a consumer should not have to rely on that alone. Filed from `core-engine` (`.scratch/composite-integration/spec.md`, user stories 29 and 34).

**Blocked by:** None — can start immediately. Amends the bars response documented by ticket 09 (`Content-Length`, and the failure mode) once done.

**Status:** ready-for-agent

- [ ] Every failure that can happen before the first byte — DuckDB `COPY`, temp dir, file open — still produces the mapped JSON error (`bars.go:74-78`); pinned by a handler test with a store stub that fails without writing
- [ ] A successful Parquet response carries `Content-Length` equal to the body's byte size (the file is fully on disk before the first byte, so the size is known); handler test asserts the header equals `len(body)`
- [ ] A failure after the first byte is detectable by a client: the connection is closed short of `Content-Length` / without the terminating chunk (e.g. `panic(http.ErrAbortHandler)`), and a handler test with a store stub that writes then fails asserts the client's `io.ReadAll` returns an error and no `200`-and-clean-EOF is possible
- [ ] The log line for a mid-stream failure (`bars.go:82-83`) still fires and carries dataset, timeframe, range and the error
- [ ] Decision recorded for acquisition's sibling route `GET /datasets/{provider}/{symbol}/{timeframe}/bars` (`internal/acquisition/adapters/httpapi/datasets.go:100-112`, same pattern): either the same treatment, or a one-line reason why not
- [ ] `ASSUMPTIONS.md:296-299` (the "cannot be taken back" paragraph) and README's `query` description updated; ticket 09's contract amended if it already exists; `where-we-are-at.md` updated

## Current behaviour at `3db181e`

**Handler** — `internal/composite/adapters/httpapi/bars.go:71-84`:

```go
w.Header().Set("Content-Type", contentTypeParquet)
counted := &countingWriter{w: w}
if err := a.svc.ExportBars(r.Context(), query, counted); err != nil {
    if counted.n == 0 {
        a.fail(w, err)   // status line unsent: normal JSON error
        return
    }
    // The body is already on the wire and the status says 200. Truncating it
    // is all the honesty left, and the log is where this is answered.
    a.log.Error("export composite parquet", ...)
}
```

- `countingWriter` (`bars.go:165-174`) only counts bytes; it carries no error to the client.
- The first `Write` implicitly sends `200` — the `recorder` wrapper forces `WriteHeader(200)` on the first write (`api.go:98-101`).
- No `Content-Length` is set, so Go sends the body `Transfer-Encoding: chunked`.
- After the log line the handler **returns normally**, and `net/http` then writes the final `0\r\n\r\n` chunk. The client sees a complete chunked body.

**Store** — `internal/composite/adapters/duckdb/query.go:72-104`: `ExportBars` runs `COPY (…) TO <tmpfile> (FORMAT PARQUET)` (`:84-91`), *then* opens the file (`:94`) and `io.Copy`s it to the writer (`:100`). So:

- every DuckDB / planning failure happens **before** the first byte and already becomes a proper JSON error with its mapped status;
- the only ways to fail *after* the first byte are a read error on the temp file or a write error on the response (the client itself went away). The window is narrow, but it is exactly the case where a consumer cannot tell a short file from a whole one;
- the whole file exists on disk before streaming starts, so its size (`file.Stat()`) is available for free before the first byte — `Content-Length` costs nothing.

**Same pattern elsewhere**: acquisition's `ExportParquet` (`internal/acquisition/adapters/duckdb/parquet.go:28-60`) and its handler (`internal/acquisition/adapters/httpapi/datasets.go:100-112`); `ASSUMPTIONS.md:296-299` documents the truncation as accepted behaviour.

**Empirical check** (Go 1.25, `httptest`, 10 KB partial body so the status line is already flushed):

| Handler behaviour after a partial write | What the client observes |
|---|---|
| return normally (today) | `200`, chunked, `io.ReadAll` → 10000 bytes, **err = nil** |
| `panic(http.ErrAbortHandler)` | `200`, chunked, `io.ReadAll` → `unexpected EOF` |
| `Content-Length: 20000` declared, 10 KB written | `200`, `io.ReadAll` → `unexpected EOF` |
| `Trailer: X-Composite-Error` declared, set after body | `200`, clean EOF, `res.Trailer["X-Composite-Error"]` populated |

Only the first row is undetectable — and it is what ships today.

## What `core-engine` does to cope

Per `core-engine/.scratch/composite-integration/spec.md` § "CompositeHttpAdapter" and its ticket `07-composite-http-adapter.md`:

- decodes every Parquet body **in full** (no streaming decode) and validates the Parquet magic (`PAR1` at both ends) and footer before yielding a single bar;
- a truncated body is an **error, never a short result** — no partial bars reach the engine (user story 29: "a Composite mid-stream error (which today still returns 200) never becomes a short but plausible-looking backtest");
- separately cross-checks `X-Composite-State` / `X-Composite-Mode` on every bars response against `describe`.

That validation stays regardless of what this ticket does — it is the consumer's belt; this ticket is the server's braces. Note that a truncated Parquet file is *also* unreadable by DuckDB / Arrow readers (missing footer), so any consumer that reads the file whole will fail loudly; the risk is consumers that stream row groups and stop early, and consumers that write the body to disk and trust the `200`.

## Options

**A. `Content-Length` from the temp file's size — recommended, cheapest.** The file is complete before streaming, so the store can report its size. Two ways to get it to the handler:
- change the port to `ExportBars(ctx, q) (io.ReadCloser, int64, error)` — the store returns the opened temp file and its size, the handler sets `Content-Length` and does the `io.Copy`, and `Close` removes the temp dir. This also makes "fail before headers" structural: every store failure happens before the handler has a reader, so nothing store-side can ever fail after the first byte; or
- keep `ExportBars(ctx, q, w)` and have the store call an optional `interface{ ContentLength(int64) }` on `w` before writing — smaller diff, less honest.
A short body then yields `unexpected EOF` on the client (row 3 of the table). Acquisition's route gets the same for free if its port is changed the same way.

**B. Abort the connection on mid-stream failure.** In the `counted.n > 0` branch, after logging, `panic(http.ErrAbortHandler)` — `net/http` closes the connection without the terminating chunk and the client's read fails (row 2). Zero API change, works with or without A, and covers the one failure A cannot express (a write error mid-`io.Copy`). Cost: one abort log line from `net/http` is suppressed for `ErrAbortHandler` by design, so the handler's own log line must carry the detail (it already does).

**C. Declared trailer.** `w.Header().Set("Trailer", "X-Composite-Error")` before the first byte, set the trailer value on failure (row 4). Signals the *reason*, not just the fact, but only over chunked encoding (so it conflicts with A) and most HTTP clients (reqwest/hyper, curl) do not surface trailers without extra work — `core-engine` would have to read them explicitly. Least portable; not recommended as the only mechanism.

**D. Buffer / pre-validate.** Already effectively the case — the store materialises the whole file before writing. Nothing to add beyond making it explicit (A does that).

Recommendation: **A + B**. A gives every well-behaved client a free, standard truncation check; B guarantees the response is never *both* `200` and cleanly terminated when the export did not finish. C can be layered on later if a consumer wants the reason on the wire.

## Out of scope

- Streaming the Parquet encoding (writing row groups as they are produced) — would remove the temp file and reintroduce true mid-stream failures; if it ever happens, B and C are the tools.
- Changing the Parquet schema, the JSON path, or the `X-*` headers (ticket 09 documents them as they are).

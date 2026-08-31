# 02 — Calendar-capable composite Timeframe

**What to build:** The composite context's own Timeframe domain type (ADR-0004): every fixed frame the dataset layer serves plus calendar frames `1w` and `1M`, with correct UTC boundary math — so that every later ticket (build, materialization, query) speaks one timeframe language that acquisition's fixed-duration type cannot.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] Parses and formats the canonical short forms including `1w` and `1M`; rejects anything else
- [x] Fixed frames align exactly as acquisition aligns them (epoch-anchored), so the two contexts never disagree about `1m`…`1d` boundaries
- [x] `1w` windows start Monday 00:00 UTC (ISO-8601); `1M` windows start on the 1st at 00:00 UTC; correct across year boundaries, leap years, and 28/29/30/31-day months
- [x] Can iterate the window start times covering any half-open Range, and answer which window a given open time belongs to
- [x] Converts to acquisition's Timeframe only for frames acquisition supports; conversion of calendar frames is impossible by construction
- [x] Pure domain code, stdlib only, exhaustively unit-tested (boundary tables for week/month starts); `go test -race ./...` green

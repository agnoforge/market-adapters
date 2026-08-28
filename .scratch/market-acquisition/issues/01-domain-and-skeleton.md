Status: done

# Domain types and module skeleton

Go module, folder layout from spec. `internal/domain`: Timeframe (parse/duration), Range (half-open, union/intersect/subtract), DatasetID, Bar with Validate(), Gap, TradingCalendar interface + Continuous impl (Expected(range) → []open_time or iterator). Table-driven tests for Range ops and Bar.Validate.

## Done when
- [x] `go build ./...` and `go vet ./...` clean; `go test ./internal/domain/...` green
- [x] `Timeframe` parses the 13 fixed-duration Binance strings, rejects `1s`/`1w`/`1M`, and exposes its duration
- [x] `Range` is half-open; union, intersect and subtract have table-driven tests incl. touching and disjoint cases
- [x] `Bar.Validate` rejects low>min(o,c), high<max(o,c), non-positive prices; accepts a normal bar
- [x] `Continuous` calendar yields exactly N expected open_times for an N-minute range at `1m`
- [x] `internal/domain` imports nothing outside stdlib

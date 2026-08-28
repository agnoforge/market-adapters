Status: todo

# Domain types and module skeleton

Go module, folder layout from spec. `internal/domain`: Timeframe (parse/duration), Range (half-open, union/intersect/subtract), DatasetID, Bar with Validate(), Gap, TradingCalendar interface + Continuous impl (Expected(range) → []open_time or iterator). Table-driven tests for Range ops and Bar.Validate.

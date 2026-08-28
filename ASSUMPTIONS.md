# Assumptions (spec silent)

- Module path: `github.com/agnos/agnoforge`.

## Ticket 01 (domain + skeleton)

- `Range` operations normalise their results to UTC, so returned ranges compare
  equal regardless of the location the caller's inputs carried.
- `Union` treats an empty range as an identity: `empty ∪ r = r`, ok. Only
  disjoint non-empty ranges report `false`.
- `Intersect` returns the zero `Range` (not an error) when there is no overlap.
- `Subtract` / `MergeRanges` / `SubtractRanges` return `nil`, not an empty
  slice, when nothing remains.
- `Bar.Validate` also rejects a zero or non-UTC `OpenTime`, and requires
  `Volume >= 0` (prices must be strictly positive). Parsing is `math/big.Rat`
  so a `DECIMAL(20,8)` value never loses precision in a comparison.
- `Bar.Validate` and `ParseGapStatus` return plain descriptive errors; the four
  sentinels in `errors.go` are the only ones the ticket calls for.
- `Timeframe.Duration()` returns `0` for an unsupported timeframe rather than
  panicking, and `Continuous.Expected` yields nothing for one.
- Calendar boundaries are computed in Unix **milliseconds** (spec: open_time is
  UTC epoch ms), which also avoids the year-2262 `UnixNano` overflow.

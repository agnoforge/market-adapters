Status: todo

# Binance klines adapter

Depends on: 01. Provider port: SupportedTimeframes (fixed-duration only), EarliestAvailable (first kline startTime=0), Continuous calendar, Bars iterator with 1000-per-page paging, token bucket 6000 weight/min (weight 2), retry ×5 backoff honouring Retry-After (429/418), clip end to last closed bar, drop invalid bars. ErrUnknownSymbol/ErrUnsupportedTimeframe permanent. Tests with recorded HTTP fixtures (httptest).

## Done when
- [ ] `SupportedTimeframes` = 1m 3m 5m 15m 30m 1h 2h 4h 6h 8h 12h 1d 3d, nothing else
- [ ] `Bars` pages at 1000 per request and yields every bar of a 2500-bar fixture range exactly once
- [ ] End is clipped to the last fully closed bar (fixture with an in-progress candle: it is never yielded)
- [ ] 429 with `Retry-After` is retried after that delay; 5 failures → error; 400 unknown symbol → `ErrUnknownSymbol` with no retry
- [ ] Token bucket: 6000 weight/min, klines weight 2, shared across concurrent callers (test with fake clock)
- [ ] `EarliestAvailable` returns the first fixture kline open_time
- [ ] Invalid bars are dropped and logged, not yielded
- [ ] No HTTP call reaches the network in tests (httptest only)

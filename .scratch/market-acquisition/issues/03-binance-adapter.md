Status: todo

# Binance klines adapter

Depends on: 01. Provider port: SupportedTimeframes (fixed-duration only), EarliestAvailable (first kline startTime=0), Continuous calendar, Bars iterator with 1000-per-page paging, token bucket 6000 weight/min (weight 2), retry ×5 backoff honouring Retry-After (429/418), clip end to last closed bar, drop invalid bars. ErrUnknownSymbol/ErrUnsupportedTimeframe permanent. Tests with recorded HTTP fixtures (httptest).

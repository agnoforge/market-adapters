# Market Data Acquisition

Acquires historical OHLCV bars from external providers exactly as the provider reported them, records what was asked for and what is missing, and serves that data to other AgnoForge services. It never derives, aggregates, or merges bars — that belongs to other modules.

## Language

**Provider**:
An external source of market data (e.g. Binance). Provider-specific behaviour lives only in that provider's adapter.
_Avoid_: exchange, source, feed

**Symbol**:
A provider-native identifier for a tradable market, spelled exactly as the provider spells it (`BTCUSDT`).
_Avoid_: ticker, pair, instrument

**Instrument**:
Reserved for the future *logical* market (BTC/USD) that maps to one Symbol per Provider. Not used in this context yet — do not use it to mean Symbol.

**Timeframe**:
The duration of one bar, written in canonical short form: `1m`, `5m`, `1h`, `1d`.
_Avoid_: interval, resolution, period

**Dataset**:
The collection of bars uniquely identified by `(Provider, Symbol, Timeframe)`. Every Dataset here is source data: it contains only bars a Provider actually returned.
_Avoid_: series, feed, table

**Bar**:
One OHLCV candle in a Dataset, identified by its `open_time` (UTC, epoch milliseconds). Only fully closed bars exist; the currently forming candle is never a Bar.
_Avoid_: candle, kline, row

**Backfill**:
A request to acquire all Bars of one Dataset over a half-open range `[start, end)`. Idempotent: running it again yields the same Dataset.
_Avoid_: download, sync, ingestion job

**Coverage**:
The union of ranges that have been requested via Backfill for a Dataset. Only inside Coverage can the system say whether data is complete.
_Avoid_: validated range, known range

**Gap**:
A contiguous range inside a Dataset's Coverage where Bars are expected but absent. Stored as a record with a status: `open`, `repaired`, `ignored`, `unrecoverable`.
_Avoid_: hole, missing data

**Settled** (Gap):
A Gap whose status is `ignored` or `unrecoverable`: an operator has decided it will not be filled, so its range is excluded from expected during gap detection. A settled Gap whose data later appears becomes `repaired`.
_Avoid_: closed, resolved

**Trading Calendar**:
The Provider's statement of which Bars are expected for a Symbol over a range. Gap detection is expected-minus-present, so a market-closed weekend is not a Gap. Binance's calendar is continuous (24/7).
_Avoid_: schedule, session hours

**Repair**:
A Backfill targeted at a Gap's range. Idempotent.

**Complete**:
A range is Complete for a Dataset when it lies entirely within Coverage and no `open` Gap intersects it. This is the only question other services should ask before trusting a range.

## Boundaries

- Higher-timeframe materialization, composite datasets, and backtesting Strict/Research modes live in other modules. Those terms are not part of this context.

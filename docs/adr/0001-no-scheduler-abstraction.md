---
status: accepted
---
# No job scheduler / message broker abstraction until a provider needs it

The PRD proposed rate-limit-aware task orchestration, persistent job state and a scheduler port that a message broker could later replace. For the only Phase 1 provider (Binance) a complete 1-minute history is ~4,700 requests at weight 2 against a 6,000-weight/min budget — minutes, not hours. We therefore run a Backfill as a plain in-process loop with a token-bucket rate limiter inside the provider adapter, and get resumability for free from Coverage + idempotent bars: "resume" is "run the same Backfill again". A scheduler port with one implementation is deferred until a provider's limits make a Backfill exceed roughly an hour.

## Considered options

- Task scheduler port + in-process implementation now (rejected: one implementation is a costume, not a seam)
- Persistent job table for resumability (rejected: Coverage already records what was asked for)

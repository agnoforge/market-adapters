---
status: accepted
---
# Composite context defines its own calendar-capable Timeframe instead of extending domain.Timeframe

The composite module must materialize `1w` and `1M` bars, but `domain.Timeframe` is deliberately fixed-duration only: acquisition's boundary alignment is integer arithmetic from the Unix epoch, and a variable-length bar cannot be aligned that way (`1w` ≠ 7d-from-epoch — the epoch is a Thursday). We keep acquisition's constraint intact and give the composite context its own Timeframe type covering fixed frames plus calendar frames, with calendar-aware boundaries fixed at UTC: weeks start Monday 00:00 (ISO-8601), months on the 1st at 00:00. The only acquisition timeframe the composite ever requests is `1m`, so translation at the context boundary is trivial.

## Considered options

- Extend `domain.Timeframe` with `1w`/`1M` (rejected: weakens acquisition's correct invariant and touches its alignment rules for a need it doesn't have)

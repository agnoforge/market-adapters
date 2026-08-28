Status: todo

# Repair and gap status changes

Depends on: 04, 05. Repair = Backfill over gap range; SetGapStatus open|ignored|unrecoverable with reason; repaired set automatically.

## Done when
- [ ] `Repair(gapID)` starts a Backfill over exactly the Gap range and returns its id
- [ ] `SetGapStatus` accepts open|ignored|unrecoverable with a reason; rejects `repaired` (only detection sets it)
- [ ] Repairing an `ignored` Gap whose data now exists sets `repaired`
- [ ] `IsComplete` becomes true after ignoring the only intersecting Gap

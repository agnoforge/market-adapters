Status: todo

# Gap detection and Complete query

Depends on: 02. DetectGaps(dataset, range): expected − present − ignored/unrecoverable, replace open gaps, mark repaired when filled. IsComplete(dataset, range). Tests cover weekend-style calendar via a fake non-continuous calendar.

## Done when
- [ ] Missing minutes inside Coverage become `open` Gaps, one record per contiguous run
- [ ] Minutes outside Coverage are never Gaps
- [ ] A fake weekend-closing calendar produces no Gap over the closed period
- [ ] Ranges of `ignored`/`unrecoverable` gaps are excluded from expected and not re-reported
- [ ] When a Gap range is fully present on re-detection its status becomes `repaired`
- [ ] `IsComplete` is true iff range ⊆ Coverage and no `open` Gap intersects; returns the intersecting gaps

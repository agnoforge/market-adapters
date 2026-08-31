package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/domain"
)

// The tests in this file are cross-provider catch-up: a dataset whose base
// provider stops before the requested end, and which was explicitly configured
// to continue with a second one. They are the requirements document's own
// scenario at the scale of an hour — the base ends mid-way, the catch-up
// provider completes the range — plus the two things the boundary between two
// providers must never be allowed to be: an overlap, or a hole.
//
// The second provider is a fake, as the spec says it will be: providers are
// opaque names in this context, and no exchange adapter is part of this work.

// handover is where the base provider stops in these tests: half past the hour.
var handover = buildStart.Add(30 * time.Minute)

// crossProvider is the declaration that permits cross-provider assembly at
// all: the same hour as every other Build test, continued by a second provider
// the researcher named on purpose.
func crossProvider(name, mode string) map[string]any {
	body := buildable(name, mode)
	body["catch_up"] = map[string]any{
		"kind":       "source",
		"instrument": instrument,
		"provider":   "coinbase",
		"symbol":     catchUpSymbol,
		"timeframe":  "1m",
	}
	return body
}

// baseStopsAt is the world the scenario starts in: the base source is acquired
// and seeded up to stop, and the base provider has nothing past it.
func baseStopsAt(t *testing.T, stop time.Time) *harness {
	t.Helper()
	h := newHarness(t)
	covered := acq.Range{Start: buildStart, End: stop}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(covered)
	return h
}

// --- the end-to-end scenario ------------------------------------------------

// TestTheCatchUpProviderCompletesTheRangeTheBaseProviderStopsInsideOf is the
// requirements document's scenario: base data to the middle of the range, a
// configured second provider for the rest, one continuous timeline out of two
// provider-attributed Segments that abut exactly.
func TestTheCatchUpProviderCompletesTheRangeTheBaseProviderStopsInsideOf(t *testing.T) {
	h := baseStopsAt(t, handover)
	tail := acq.Range{Start: handover, End: buildEnd}
	h.catchUpProviderServes(tail)
	h.create(crossProvider("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}

	// Two Segments, in timeline order, each naming the provider, the symbol
	// and the range it answers for.
	if len(built.Segments) != 2 {
		t.Fatalf("segments = %+v, want a base one and a catch-up one", built.Segments)
	}
	base, catchUp := built.Segments[0], built.Segments[1]
	if base.Kind != "base" || catchUp.Kind != "catch_up" {
		t.Errorf("segment kinds = %q, %q, want base then catch_up", base.Kind, catchUp.Kind)
	}
	wantBase := sourceJSON{Instrument: instrument, Provider: "binance", Symbol: baseSymbol, Timeframe: "1m"}
	wantCatchUp := sourceJSON{Instrument: instrument, Provider: "coinbase", Symbol: catchUpSymbol, Timeframe: "1m"}
	if base.Source != wantBase || catchUp.Source != wantCatchUp {
		t.Errorf("segment sources = %+v, %+v, want %+v then %+v",
			base.Source, catchUp.Source, wantBase, wantCatchUp)
	}
	if base.Start != "2024-01-01T00:00:00Z" || base.End != "2024-01-01T00:30:00Z" {
		t.Errorf("base segment = %s .. %s, want the half the base provider has", base.Start, base.End)
	}
	if catchUp.Start != "2024-01-01T00:30:00Z" || catchUp.End != "2024-01-01T01:00:00Z" {
		t.Errorf("catch-up segment = %s .. %s, want the tail it completed", catchUp.Start, catchUp.End)
	}
	// Abutting exactly on the half-open boundary is what makes the two one
	// timeline: the base ends where the catch-up begins, to the minute.
	if base.End != catchUp.Start {
		t.Errorf("the segments meet at %s and %s, want one instant", base.End, catchUp.Start)
	}

	// The timeline is complete, and its bars really are there — 30 from each
	// provider, counted in acquisition's own table.
	q := built.Quality
	if q.AvailableStart != "2024-01-01T00:00:00Z" || q.AvailableEnd != "2024-01-01T01:00:00Z" {
		t.Errorf("available range = %s .. %s, want the whole requested hour", q.AvailableStart, q.AvailableEnd)
	}
	if q.ActualBars != 60 || q.ExpectedBars != 60 || q.CoveragePercentage != 100 {
		t.Errorf("bars = %d of %d (%v%%), want 60 of 60 at 100%%",
			q.ActualBars, q.ExpectedBars, q.CoveragePercentage)
	}
	if q.TransitionCount != 1 || len(q.Transitions) != 1 {
		t.Fatalf("transitions = %+v, want the one provider boundary", q.Transitions)
	}
	if q.Transitions[0].At != "2024-01-01T00:30:00Z" {
		t.Errorf("transition at %q, want the boundary the segments meet on", q.Transitions[0].At)
	}

	// Only two sources were ever asked for anything, and each only for what it
	// was there for: the base provider for its own tail, the catch-up provider
	// for what the base could not supply. There is no third request and no
	// chain past those two.
	if got := h.port.requestedRangesOf(baseSource); len(got) != 1 || got[0] != tail {
		t.Fatalf("the base provider was asked over %v, want the missing tail %s first", got, tail)
	}
	if got := h.port.requestedRangesOf(catchUpSource); len(got) != 1 || got[0] != tail {
		t.Fatalf("the catch-up provider was asked over %v, want exactly the tail %s", got, tail)
	}
	if got := h.port.attemptCount(); got != 2 {
		t.Errorf("backfill requests = %d, want 2 — the base provider then the catch-up one, and nothing else", got)
	}

	// And it is all persisted: Get is the provenance API.
	read := h.get("btc-usd")
	if len(read.Segments) != 2 || read.Segments[0] != base || read.Segments[1] != catchUp {
		t.Fatalf("get segments = %+v, want the two the build assembled", read.Segments)
	}
	if read.Quality == nil || read.Quality.TransitionCount != 1 {
		t.Fatalf("get quality = %+v, want the transition recorded", read.Quality)
	}
}

// TestTheBaseProviderIsExtendedFirstAndTheCatchUpProviderTakesOnlyTheRemainder:
// the base provider can still serve part of the tail, so it does — and the
// catch-up provider is asked for the remainder alone, never for what the base
// genuinely could supply.
func TestTheBaseProviderIsExtendedFirstAndTheCatchUpProviderTakesOnlyTheRemainder(t *testing.T) {
	h := baseStopsAt(t, handover)
	reach := buildStart.Add(45 * time.Minute)
	// The base provider has fifteen more minutes in it than acquisition holds.
	h.providerServes(acq.Range{Start: buildStart, End: reach})
	remainder := acq.Range{Start: reach, End: buildEnd}
	h.catchUpProviderServes(remainder)
	h.create(crossProvider("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if got := h.port.backfilledOf(baseSource); len(got) != 1 {
		t.Fatalf("the base provider filled %v, want it extended first", got)
	}
	if got := h.port.requestedRangesOf(catchUpSource); len(got) != 1 || got[0] != remainder {
		t.Fatalf("the catch-up provider was asked over %v, want only the remainder %s", got, remainder)
	}
	if len(built.Segments) != 2 {
		t.Fatalf("segments = %+v, want two", built.Segments)
	}
	if built.Segments[0].End != "2024-01-01T00:45:00Z" || built.Segments[1].Start != "2024-01-01T00:45:00Z" {
		t.Errorf("the segments meet at %s / %s, want 00:45 — as far as the base provider goes",
			built.Segments[0].End, built.Segments[1].Start)
	}
	if built.Quality.ActualBars != 60 {
		t.Errorf("actual bars = %d, want the 60 of the completed hour", built.Quality.ActualBars)
	}
}

// --- the two boundaries that are refused ------------------------------------

// TestOverlappingSourceDataFailsTheBuild: the catch-up source already holds
// data reaching back into the base segment. There is one merge policy and it is
// reject_conflict — conflicting provider data is surfaced, never merged.
func TestOverlappingSourceDataFailsTheBuild(t *testing.T) {
	h := baseStopsAt(t, handover)
	overlapping := acq.Range{Start: buildStart.Add(20 * time.Minute), End: buildEnd}
	h.port.coverOf(catchUpSource, overlapping)
	h.seedBarsOf(catchUpSource, overlapping)
	h.create(crossProvider("btc-usd", "strict"))

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	for _, want := range []string{"binance", "coinbase", "2024-01-01T00:20:00Z", "reject_conflict"} {
		if !strings.Contains(message, want) {
			t.Errorf("the failure said %q, want it to name %q", message, want)
		}
	}

	got := h.get("btc-usd")
	if got.State != "failed" || got.LastError == "" {
		t.Fatalf("state = %q with error %q, want failed with the reason preserved", got.State, got.LastError)
	}
	if !strings.Contains(got.LastError, "reject_conflict") {
		t.Errorf("last error = %q, want the conflict preserved", got.LastError)
	}
	if len(got.Segments) != 0 {
		t.Errorf("segments = %+v, want none — nothing was silently accepted", got.Segments)
	}
}

// TestATimestampHoleBetweenTheProvidersFailsTheBuild: the catch-up provider's
// data starts later than the base one stops, so the assembled timeline would
// claim minutes nothing supplies. That is surfaced, not accepted.
func TestATimestampHoleBetweenTheProvidersFailsTheBuild(t *testing.T) {
	h := baseStopsAt(t, handover)
	// The catch-up provider has nothing before 00:40.
	late := acq.Range{Start: buildStart.Add(40 * time.Minute), End: buildEnd}
	h.catchUpProviderServes(late)
	h.create(crossProvider("btc-usd", "research"))

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	for _, want := range []string{"2024-01-01T00:30:00Z", "2024-01-01T00:40:00Z", "abut"} {
		if !strings.Contains(message, want) {
			t.Errorf("the failure said %q, want it to name %q", message, want)
		}
	}
	// The tail was asked for in full: the provider's floor is the reason for
	// the hole, not an omission by the Build.
	tail := acq.Range{Start: handover, End: buildEnd}
	if got := h.port.requestedRangesOf(catchUpSource); len(got) != 1 || got[0] != tail {
		t.Errorf("the catch-up provider was asked over %v, want the whole tail %s", got, tail)
	}
	got := h.get("btc-usd")
	if got.State != "failed" || got.LastError == "" {
		t.Fatalf("state = %q with error %q, want failed with the reason preserved", got.State, got.LastError)
	}
	if len(got.Segments) != 0 {
		t.Errorf("segments = %+v, want none — a holed timeline is not a timeline", got.Segments)
	}
}

// --- the price delta across the transition ----------------------------------

// TestTheCloseToOpenDeltaAcrossTheTransitionIsRecorded: the two providers
// disagree by a quarter at the seam. The movement is recorded exactly — as the
// decimals the bars hold — and nothing is enforced: the dataset is ready.
func TestTheCloseToOpenDeltaAcrossTheTransitionIsRecorded(t *testing.T) {
	h := newHarness(t)
	// The prices are set before anything is seeded, so both the seeded bars and
	// the ones a backfill lands are worth what this test says they are.
	h.priceBarsOf(baseSource, "100.50000000")
	h.priceBarsOf(catchUpSource, "100.75000000")
	covered := acq.Range{Start: buildStart, End: handover}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(covered)
	h.catchUpProviderServes(acq.Range{Start: handover, End: buildEnd})
	h.create(crossProvider("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready — a price delta is recorded, never enforced (error %q)",
			built.State, built.LastError)
	}
	if built.Quality.TransitionCount != 1 || len(built.Quality.Transitions) != 1 {
		t.Fatalf("transitions = %+v, want the one provider boundary", built.Quality.Transitions)
	}
	got := built.Quality.Transitions[0]
	if got.At != "2024-01-01T00:30:00Z" {
		t.Errorf("transition at %q, want the boundary", got.At)
	}
	wantFrom := sourceJSON{Instrument: instrument, Provider: "binance", Symbol: baseSymbol, Timeframe: "1m"}
	wantTo := sourceJSON{Instrument: instrument, Provider: "coinbase", Symbol: catchUpSymbol, Timeframe: "1m"}
	if got.From != wantFrom || got.To != wantTo {
		t.Errorf("transition = %+v → %+v, want %+v → %+v", got.From, got.To, wantFrom, wantTo)
	}
	// The last close of the base source, the first open of the catch-up one,
	// and their exact difference — decimal strings, to the last place.
	if got.Close != "100.50000000" || got.Open != "100.75000000" {
		t.Errorf("close → open = %q → %q, want 100.50000000 → 100.75000000", got.Close, got.Open)
	}
	if got.Delta != "0.25000000" {
		t.Errorf("price delta = %q, want exactly 0.25000000", got.Delta)
	}

	// It is persisted with the rest of the Quality, so the seam stays visible
	// after the build that crossed it.
	read := h.get("btc-usd")
	if read.Quality == nil || len(read.Quality.Transitions) != 1 {
		t.Fatalf("get quality = %+v, want the transition recorded", read.Quality)
	}
	if read.Quality.Transitions[0].Delta != "0.25000000" {
		t.Errorf("delta read back = %q, want the exact decimal", read.Quality.Transitions[0].Delta)
	}
}

// --- no catch-up provider ---------------------------------------------------

// TestWithoutAConfiguredCatchUpProviderTheBuildIsBaseOnly: the second
// provider's data is right there in acquisition's table, and it is never
// touched. Cross-provider assembly is impossible unless it was configured.
func TestWithoutAConfiguredCatchUpProviderTheBuildIsBaseOnly(t *testing.T) {
	h := baseStopsAt(t, handover)
	tail := acq.Range{Start: handover, End: buildEnd}
	h.port.coverOf(catchUpSource, tail)
	h.seedBarsOf(catchUpSource, tail)
	h.catchUpProviderServes(tail)
	// The declaration says nothing about catch-up, so it is the base provider's
	// own tail — which the base provider does not have.
	h.create(buildable("btc-usd", "research"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if len(built.Segments) != 1 || built.Segments[0].Source.Provider != "binance" {
		t.Fatalf("segments = %+v, want the one base segment", built.Segments)
	}
	if built.Segments[0].End != "2024-01-01T00:30:00Z" {
		t.Errorf("segment end = %q, want where the base provider stops", built.Segments[0].End)
	}
	if built.Quality.ActualBars != 30 || built.Quality.CoveragePercentage != 50 {
		t.Errorf("bars = %d (%v%%), want the base provider's 30 at 50%%",
			built.Quality.ActualBars, built.Quality.CoveragePercentage)
	}
	if built.Quality.TransitionCount != 0 || len(built.Quality.Transitions) != 0 {
		t.Errorf("transitions = %+v, want none — one provider crossed no boundary", built.Quality.Transitions)
	}
	if got := h.port.requestedRangesOf(catchUpSource); len(got) != 0 {
		t.Fatalf("the second provider was asked over %v, want never — it was not configured", got)
	}
}

// TestAConfiguredCatchUpProviderIsNotAskedWhenTheBaseReachesTheResolvedEnd: a
// dataset stays as single-source as it can be. The catch-up provider exists for
// the tail, and there is no tail.
func TestAConfiguredCatchUpProviderIsNotAskedWhenTheBaseReachesTheResolvedEnd(t *testing.T) {
	h := newHarness(t)
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, buildRange)
	h.catchUpProviderServes(buildRange)
	h.create(crossProvider("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if len(built.Segments) != 1 || built.Segments[0].Kind != "base" {
		t.Fatalf("segments = %+v, want the one base segment", built.Segments)
	}
	if built.Quality.TransitionCount != 0 {
		t.Errorf("transitions = %d, want none", built.Quality.TransitionCount)
	}
	if got := h.port.requestedRangesOf(catchUpSource); len(got) != 0 {
		t.Fatalf("the catch-up provider was asked over %v, want never — the base reached the end", got)
	}
}

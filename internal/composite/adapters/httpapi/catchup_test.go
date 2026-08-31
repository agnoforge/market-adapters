package httpapi_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
)

// The tests in this file are the catch-up half of a Build: the requested range
// exceeds what the base source has been acquired over, so the Build asks
// acquisition to extend the base provider's coverage — head and tail — and
// waits for it before it assembles anything. The fake port is acquisition's
// control plane; the bars a backfill lands are written into acquisition's own
// table, because no port carries a bar (ADR-0005).

// --- nothing to catch up ----------------------------------------------------

// TestABuildOverAlreadyCompleteCoverageRequestsNoBackfill is the case that must
// stay cheap: everything asked for is already there, so acquisition is asked
// for nothing beyond coverage and completeness.
func TestABuildOverAlreadyCompleteCoverageRequestsNoBackfill(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	// The provider could serve the whole hour — it is simply never asked to.
	h.providerServes(buildRange)

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if got := h.port.requestedRanges(); len(got) != 0 {
		t.Errorf("backfill requests = %v, want none — the range was already covered", got)
	}
	if h.port.attemptCount() != 0 {
		t.Errorf("backfill requests = %d, want none", h.port.attemptCount())
	}
	if h.port.calls() != 0 {
		t.Errorf("the build reached %d capabilities it should not need", h.port.calls())
	}
}

// --- filling the tail -------------------------------------------------------

func TestBuildBackfillsTheTailTheBaseSourceIsMissing(t *testing.T) {
	h := newHarness(t)
	covered := acq.Range{Start: buildStart, End: buildStart.Add(30 * time.Minute)}
	tail := acq.Range{Start: buildStart.Add(30 * time.Minute), End: buildEnd}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(buildRange)
	h.create(buildable("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if got := h.port.backfilled(); len(got) != 1 || got[0] != tail {
		t.Fatalf("backfills = %v, want exactly the missing tail %s", got, tail)
	}
	// The build waited for it: the segment, the quality and the bars all
	// describe the extended hour, not the half it started from.
	if built.Segments[0].End != "2024-01-01T01:00:00Z" {
		t.Errorf("segment end = %q, want the backfilled end", built.Segments[0].End)
	}
	if built.Quality.AvailableEnd != "2024-01-01T01:00:00Z" {
		t.Errorf("available end = %q, want the backfilled end", built.Quality.AvailableEnd)
	}
	if built.Quality.ActualBars != 60 || built.Quality.ExpectedBars != 60 {
		t.Errorf("bars = %d of %d, want 60 of 60 — the backfill's bars landed before assembly",
			built.Quality.ActualBars, built.Quality.ExpectedBars)
	}
}

// --- filling the head -------------------------------------------------------

func TestBuildBackfillsTheHeadTowardTheRequestedStart(t *testing.T) {
	h := newHarness(t)
	covered := acq.Range{Start: buildStart.Add(20 * time.Minute), End: buildEnd}
	head := acq.Range{Start: buildStart, End: buildStart.Add(20 * time.Minute)}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(buildRange)
	h.create(buildable("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if got := h.port.backfilled(); len(got) != 1 || got[0] != head {
		t.Fatalf("backfills = %v, want exactly the missing head %s", got, head)
	}
	if built.Segments[0].Start != "2024-01-01T00:00:00Z" {
		t.Errorf("segment start = %q, want the requested start the head reached", built.Segments[0].Start)
	}
	if built.Quality.AvailableStart != "2024-01-01T00:00:00Z" {
		t.Errorf("available start = %q, want the requested start", built.Quality.AvailableStart)
	}
	if built.Quality.ActualBars != 60 {
		t.Errorf("actual bars = %d, want the 60 of the whole hour", built.Quality.ActualBars)
	}
}

// TestBuildBackfillsBothEndsOfTheRequestedRange is the head and the tail at
// once: the missing parts are two, and both are asked for.
func TestBuildBackfillsBothEndsOfTheRequestedRange(t *testing.T) {
	h := newHarness(t)
	covered := acq.Range{Start: buildStart.Add(20 * time.Minute), End: buildStart.Add(40 * time.Minute)}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(buildRange)
	h.create(buildable("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	want := []acq.Range{
		{Start: buildStart, End: buildStart.Add(20 * time.Minute)},
		{Start: buildStart.Add(40 * time.Minute), End: buildEnd},
	}
	got := h.port.backfilled()
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("backfills = %v, want the head and the tail %v", got, want)
	}
	if built.Quality.ActualBars != 60 {
		t.Errorf("actual bars = %d, want 60", built.Quality.ActualBars)
	}
}

// --- the head the provider does not have ------------------------------------

// TestStrictBuildIsNotReadyWhenTheProviderFloorLimitsTheHead: the base provider
// is asked for the head all the same, and its earliest-available floor is what
// answers. Strict refuses, naming the shortfall.
func TestStrictBuildIsNotReadyWhenTheProviderFloorLimitsTheHead(t *testing.T) {
	h := newHarness(t)
	floor := buildStart.Add(20 * time.Minute)
	covered := acq.Range{Start: floor, End: buildEnd}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	// The provider has nothing before the floor: the head cannot be filled.
	h.providerServes(covered)
	h.create(buildable("btc-usd", "strict"))

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	if !strings.Contains(message, "2024-01-01T00:00:00Z") || !strings.Contains(message, "2024-01-01T00:20:00Z") {
		t.Errorf("the failure said %q, want it to report the head shortfall", message)
	}
	// It was asked for: the floor is the reason, not an omission.
	head := acq.Range{Start: buildStart, End: floor}
	if got := h.port.requestedRanges(); len(got) != 1 || got[0] != head {
		t.Fatalf("backfill requests = %v, want the head %s to have been requested", got, head)
	}
	if got := h.port.backfilled(); len(got) != 0 {
		t.Errorf("admitted backfills = %v, want none — the provider had nothing there", got)
	}
	got := h.get("btc-usd")
	if got.State != "failed" || got.LastError == "" {
		t.Fatalf("state = %q with error %q, want failed with the error preserved", got.State, got.LastError)
	}
	if got.Quality.AvailableStart != "2024-01-01T00:20:00Z" {
		t.Errorf("available start = %q, want where the provider's data starts", got.Quality.AvailableStart)
	}
}

// TestResearchBuildStartsWhereTheProviderFloorAllows is the research half of
// the same shortfall: the dataset starts where the data starts, and Quality
// records it.
func TestResearchBuildStartsWhereTheProviderFloorAllows(t *testing.T) {
	h := newHarness(t)
	floor := buildStart.Add(20 * time.Minute)
	covered := acq.Range{Start: floor, End: buildEnd}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(covered)
	h.create(buildable("btc-usd", "research"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q)", built.State, built.LastError)
	}
	if got := h.port.requestedRanges(); len(got) != 1 {
		t.Fatalf("backfill requests = %v, want the head to have been requested once", got)
	}
	if built.Quality.AvailableStart != "2024-01-01T00:20:00Z" {
		t.Errorf("available start = %q, want where the data starts", built.Quality.AvailableStart)
	}
	if built.Segments[0].Start != "2024-01-01T00:20:00Z" {
		t.Errorf("segment start = %q, want where the data starts", built.Segments[0].Start)
	}
	if built.Quality.RequestedStart != "2024-01-01T00:00:00Z" {
		t.Errorf("requested start = %q, want the declared one preserved", built.Quality.RequestedStart)
	}
	if built.Quality.ActualBars != 40 || built.Quality.ExpectedBars != 60 {
		t.Errorf("bars = %d of %d, want 40 of 60", built.Quality.ActualBars, built.Quality.ExpectedBars)
	}
}

// TestBuildFillsWhatTheProviderHasOfTheHead: a floor inside the missing head
// still moves the dataset's start earlier, up to the floor.
func TestBuildFillsWhatTheProviderHasOfTheHead(t *testing.T) {
	h := newHarness(t)
	covered := acq.Range{Start: buildStart.Add(30 * time.Minute), End: buildEnd}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(acq.Range{Start: buildStart.Add(10 * time.Minute), End: buildEnd})
	h.create(buildable("btc-usd", "research"))

	built := h.build("btc-usd", http.StatusOK)
	if built.Quality.AvailableStart != "2024-01-01T00:10:00Z" {
		t.Fatalf("available start = %q, want the floor the backfill reached", built.Quality.AvailableStart)
	}
	if built.Quality.ActualBars != 50 {
		t.Errorf("actual bars = %d, want the 50 that are there after the partial head fill", built.Quality.ActualBars)
	}
}

// --- a backfill that fails --------------------------------------------------

func TestATerminallyFailedBackfillFailsTheBuildWithTheAcquisitionError(t *testing.T) {
	h := newHarness(t)
	covered := acq.Range{Start: buildStart, End: buildStart.Add(30 * time.Minute)}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(buildRange)
	h.port.failsWith(errors.New("binance rejected the request: 418 teapot"))
	h.create(buildable("btc-usd", "research"))

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusInternalServerError)
	if !strings.Contains(message, "418 teapot") {
		t.Errorf("the failure said %q, want the acquisition error preserved", message)
	}

	got := h.get("btc-usd")
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed — a backfill that cannot run is a build that cannot run", got.State)
	}
	if !strings.Contains(got.LastError, "418 teapot") {
		t.Errorf("last error = %q, want the acquisition error preserved", got.LastError)
	}
	if got.Quality != nil {
		t.Errorf("quality = %+v, want none — the build never got far enough to judge anything", got.Quality)
	}
}

// --- the concurrent-backfill race -------------------------------------------

// TestAConcurrentBackfillIsWaitedOutRatherThanFailingTheBuild: acquisition
// refuses a second backfill of one source Dataset. That is not a build failure
// — the Build waits for the running one and asks again.
func TestAConcurrentBackfillIsWaitedOutRatherThanFailingTheBuild(t *testing.T) {
	h := newHarness(t)
	covered := acq.Range{Start: buildStart, End: buildStart.Add(30 * time.Minute)}
	h.port.cover(covered)
	h.seedBars("binance", baseSymbol, covered)
	h.providerServes(buildRange)
	// Two refusals stand between the build and its backfill.
	h.port.busyFor(2)
	h.create(buildable("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q) — a busy source is waited out, not failed", built.State, built.LastError)
	}
	if h.port.attemptCount() != 3 {
		t.Errorf("backfill requests = %d, want 3: two refused, one admitted", h.port.attemptCount())
	}
	if built.Quality.ActualBars != 60 {
		t.Errorf("actual bars = %d, want 60 — the retried backfill landed", built.Quality.ActualBars)
	}
}

// --- gaps inside the ensured range ------------------------------------------

func TestGapsInsideTheEnsuredRangeAreRepairedBeforeReadinessIsJudged(t *testing.T) {
	h := newHarness(t)
	gap := acq.Range{Start: buildStart.Add(10 * time.Minute), End: buildStart.Add(15 * time.Minute)}
	h.port.cover(buildRange)
	h.seedBars("binance", baseSymbol, acq.Range{Start: buildStart, End: gap.Start})
	h.seedBars("binance", baseSymbol, acq.Range{Start: gap.End, End: buildEnd})
	h.port.openGap(7, gap)
	// The provider does have those five minutes; the repair is what fetches them.
	h.providerServes(buildRange)
	h.create(buildable("btc-usd", "strict"))

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready (error %q) — the gap was repairable", built.State, built.LastError)
	}
	if built.Quality.OpenGapCount != 0 {
		t.Errorf("open gaps = %+v, want none left after the repair", built.Quality.OpenGaps)
	}
	if got := h.port.backfilled(); len(got) != 1 || got[0] != gap {
		t.Fatalf("backfills = %v, want exactly the gap %s repaired", got, gap)
	}
	if built.Quality.ActualBars != 60 {
		t.Errorf("actual bars = %d, want 60 — the repaired bars are counted", built.Quality.ActualBars)
	}
}

// TestAGapTheProviderCannotFillStillBlocksAStrictBuild: the repair is attempted
// and the Gap survives it, so ticket 03's rule decides the ending.
func TestAGapTheProviderCannotFillStillBlocksAStrictBuild(t *testing.T) {
	h := readyDataset(t, "btc-usd", "strict")
	gap := acq.Range{Start: buildStart.Add(10 * time.Minute), End: buildStart.Add(15 * time.Minute)}
	h.port.openGap(7, gap)
	// The provider has nothing to serve, so the repair changes nothing.
	h.providerServes(acq.Range{})

	message := h.expectError(h.do("POST", "/composites/btc-usd/build", nil), http.StatusConflict)
	if !strings.Contains(message, "2024-01-01T00:10:00Z") {
		t.Errorf("the failure said %q, want it to name the gap that survived the repair", message)
	}
	if got := h.get("btc-usd"); got.Quality.OpenGapCount != 1 {
		t.Fatalf("open gaps = %+v, want the one that blocked readiness", got.Quality.OpenGaps)
	}
}

// TestAResearchDatasetIsReadyWithAGapTheRepairCouldNotClose is the research
// half: the same unrepairable Gap is recorded, not refused.
func TestAResearchDatasetIsReadyWithAGapTheRepairCouldNotClose(t *testing.T) {
	h := readyDataset(t, "btc-usd", "research")
	h.port.openGap(7, acq.Range{Start: buildStart.Add(10 * time.Minute), End: buildStart.Add(15 * time.Minute)})
	h.providerServes(acq.Range{})

	built := h.build("btc-usd", http.StatusOK)
	if built.State != "ready" {
		t.Fatalf("state = %q, want ready", built.State)
	}
	if built.Quality.OpenGapCount != 1 || built.Quality.OpenGaps[0].ID != 7 {
		t.Fatalf("open gaps = %+v, want the one the repair could not close", built.Quality.OpenGaps)
	}
}

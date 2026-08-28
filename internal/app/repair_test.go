package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/agnos/agnoforge/internal/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// holed seeds a Dataset that was asked for [0, 60) but is missing the Bars at
// offsets 10..14, detects its Gaps, and returns the single open Gap that
// leaves. The Provider it is built on serves exactly those five Bars, so a
// Repair of that Gap lands them.
func holed(t *testing.T, store *duckdb.Store, svc *app.Service) domain.Gap {
	t.Helper()
	ds := dataset("fake")
	seedCoverage(t, store, ds, rng(0, 60))
	seedBars(t, store, ds, testBarsAt(origin, tf, except(60, 10, 11, 12, 13, 14)...))

	gaps := detect(t, svc, ds, rng(0, 60))
	assertGaps(t, gaps, rng(10, 15))
	return gaps[0]
}

// repairProvider serves the five Bars the hole in holed is missing.
func repairProvider() *fakeProvider {
	return provider("fake", fakePage{bars: testBars(at(10), tf, 5)})
}

func TestRepairStartsABackfillOverExactlyTheGapRange(t *testing.T) {
	store := newStore(t)
	p := repairProvider()
	svc := newService(store, p)
	gap := holed(t, store, svc)

	started, err := svc.Repair(context.Background(), gap.ID)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}

	if !hexID.MatchString(string(started.ID)) {
		t.Errorf("id = %q, want 32 hex characters", started.ID)
	}
	if started.Range != gap.Range {
		t.Errorf("range = %s, want the gap range %s", started.Range, gap.Range)
	}
	if started.Dataset != gap.Dataset {
		t.Errorf("dataset = %s, want %s", started.Dataset, gap.Dataset)
	}

	final, ok := svc.Wait(started.ID)
	if !ok {
		t.Fatal("Wait: backfill not found")
	}
	if final.State != app.StateCompleted {
		t.Fatalf("state = %q, want %q (%s)", final.State, app.StateCompleted, final.LastError)
	}
	if got := p.requestedRange(); got != gap.Range {
		t.Errorf("provider asked for %s, want exactly the gap range %s", got, gap.Range)
	}
}

func TestRepairMarksAFilledGapRepaired(t *testing.T) {
	store := newStore(t)
	svc := newService(store, repairProvider())
	gap := holed(t, store, svc)

	started, err := svc.Repair(context.Background(), gap.ID)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if _, ok := svc.Wait(started.ID); !ok {
		t.Fatal("Wait: backfill not found")
	}

	if got := gapStatus(t, store, gap.ID).Status; got != domain.GapRepaired {
		t.Errorf("status = %q, want %q", got, domain.GapRepaired)
	}
	if got := len(barsIn(t, store, gap.Dataset, gap.Range)); got != 5 {
		t.Errorf("%d bars in the gap range, want 5", got)
	}
}

func TestRepairOfAnIgnoredGapWhoseDataNowExistsSetsRepaired(t *testing.T) {
	store := newStore(t)
	svc := newService(store, repairProvider())
	gap := holed(t, store, svc)
	setStatus(t, store, gap.ID, domain.GapIgnored, "provider outage, accepted")

	started, err := svc.Repair(context.Background(), gap.ID)
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if _, ok := svc.Wait(started.ID); !ok {
		t.Fatal("Wait: backfill not found")
	}

	if got := gapStatus(t, store, gap.ID).Status; got != domain.GapRepaired {
		t.Errorf("status = %q, want %q", got, domain.GapRepaired)
	}
}

func TestRepairIsIdempotent(t *testing.T) {
	store := newStore(t)
	svc := newService(store, repairProvider())
	gap := holed(t, store, svc)

	for i := range 2 {
		started, err := svc.Repair(context.Background(), gap.ID)
		if err != nil {
			t.Fatalf("Repair %d: %v", i, err)
		}
		if _, ok := svc.Wait(started.ID); !ok {
			t.Fatalf("Wait %d: backfill not found", i)
		}
	}

	if got := len(barsIn(t, store, gap.Dataset, rng(0, 60))); got != 60 {
		t.Errorf("%d bars, want 60", got)
	}
	if got := gapsOf(t, store, gap.Dataset); len(got) != 1 || got[0].Status != domain.GapRepaired {
		t.Errorf("gaps = %v, want one repaired gap", got)
	}
}

func TestRepairOfAnUnknownGapIsNotFound(t *testing.T) {
	svc := newService(newStore(t), repairProvider())

	_, err := svc.Repair(context.Background(), 424242)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
	}
}

func TestRepairReportsBackfillRunningForABusyDataset(t *testing.T) {
	store := newStore(t)
	p := repairProvider()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	p.onPage = func(int) {
		once.Do(func() { close(entered) })
		<-release
	}
	svc := newService(store, p)
	gap := holed(t, store, svc)

	started, err := svc.StartBackfill(context.Background(), request("fake", 60))
	if err != nil {
		t.Fatalf("StartBackfill: %v", err)
	}
	<-entered

	_, err = svc.Repair(context.Background(), gap.ID)
	if !errors.Is(err, domain.ErrBackfillRunning) {
		t.Errorf("err = %v, want %v", err, domain.ErrBackfillRunning)
	}

	close(release)
	if _, ok := svc.Wait(started.ID); !ok {
		t.Fatal("Wait: backfill not found")
	}
}

func TestSetGapStatus(t *testing.T) {
	tests := []struct {
		name   string
		status domain.GapStatus
		reason string
		want   error
	}{
		{name: "open", status: domain.GapOpen, reason: "reopened for another attempt"},
		{name: "ignored", status: domain.GapIgnored, reason: "binance maintenance window"},
		{name: "unrecoverable", status: domain.GapUnrecoverable, reason: "before the symbol listed"},
		{name: "empty reason", status: domain.GapIgnored},
		{name: "repaired", status: domain.GapRepaired, reason: "no", want: app.ErrGapStatusNotSettable},
		{name: "unknown", status: domain.GapStatus("bogus"), reason: "no", want: app.ErrGapStatusNotSettable},
		{name: "empty", status: domain.GapStatus(""), want: app.ErrGapStatusNotSettable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore(t)
			svc := newService(store, repairProvider())
			gap := holed(t, store, svc)

			err := svc.SetGapStatus(context.Background(), gap.ID, tt.status, tt.reason)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}

			got := gapStatus(t, store, gap.ID)
			if tt.want != nil {
				// A refused status leaves the Gap exactly as it was.
				if got.Status != domain.GapOpen || got.Reason != "" {
					t.Errorf("gap = {%q, %q}, want it untouched {%q, \"\"}", got.Status, got.Reason, domain.GapOpen)
				}
				return
			}
			if got.Status != tt.status {
				t.Errorf("status = %q, want %q", got.Status, tt.status)
			}
			if got.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", got.Reason, tt.reason)
			}
		})
	}
}

func TestSetGapStatusOfAnUnknownGapIsNotFound(t *testing.T) {
	svc := newService(newStore(t), repairProvider())

	err := svc.SetGapStatus(context.Background(), 424242, domain.GapIgnored, "nobody home")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want %v", err, domain.ErrNotFound)
	}
}

func TestIsCompleteBecomesTrueAfterIgnoringTheOnlyGap(t *testing.T) {
	store := newStore(t)
	svc := newService(store, repairProvider())
	gap := holed(t, store, svc)

	before, err := svc.IsComplete(context.Background(), gap.Dataset, rng(0, 60))
	if err != nil {
		t.Fatalf("IsComplete: %v", err)
	}
	if before.Complete || len(before.Gaps) != 1 {
		t.Fatalf("before = %+v, want incomplete with one gap", before)
	}

	if err := svc.SetGapStatus(context.Background(), gap.ID, domain.GapIgnored, "accepted"); err != nil {
		t.Fatalf("SetGapStatus: %v", err)
	}

	after, err := svc.IsComplete(context.Background(), gap.Dataset, rng(0, 60))
	if err != nil {
		t.Fatalf("IsComplete: %v", err)
	}
	if !after.Complete || len(after.Gaps) != 0 {
		t.Errorf("after = %+v, want complete with no gaps", after)
	}
}

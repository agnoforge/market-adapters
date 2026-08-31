package acqport

import (
	"errors"
	"fmt"
	"testing"

	acqapp "github.com/agnos/agnoforge/internal/app"
	compositeapp "github.com/agnos/agnoforge/internal/composite/app"
	acq "github.com/agnos/agnoforge/internal/domain"
)

// A Build absorbs two answers to a backfill request that are not failures: the
// source Dataset already has a backfill running, and the provider has nothing
// in the range. It recognises them by this context's own sentinels, so what
// this adapter translates them into is the whole reason the orchestration works
// against the real acquisition service and not only against a fake.
func TestTheRefusalsABuildAbsorbsReachItInThisContextsWords(t *testing.T) {
	cases := []struct {
		name  string
		given error
		want  error
	}{
		{
			name:  "a backfill of the same source dataset is already running",
			given: fmt.Errorf("%w: binance BTCUSDT 1m", acq.ErrBackfillRunning),
			want:  compositeapp.ErrBackfillBusy,
		},
		{
			name:  "the provider has nothing to acquire in the range",
			given: fmt.Errorf("%w: nothing to acquire in [a,b)", acqapp.ErrEmptyRange),
			want:  compositeapp.ErrNothingToAcquire,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := requestErr(tc.given)
			if !errors.Is(got, tc.want) {
				t.Fatalf("err = %v, want it to be %v", got, tc.want)
			}
			// Acquisition's own error survives the translation: nothing about
			// why it was refused is lost on the way across.
			if !errors.Is(got, tc.given) {
				t.Errorf("err = %v, want acquisition's own error preserved inside it", got)
			}
		})
	}
}

// Everything else is a failure to ask, and it crosses unchanged: a Build that
// could not ask has judged nothing.
func TestAnyOtherAcquisitionFailureCrossesUnchanged(t *testing.T) {
	failure := errors.New("the database is gone")
	got := requestErr(failure)
	if !errors.Is(got, failure) || got.Error() != failure.Error() {
		t.Fatalf("err = %v, want the acquisition failure itself", got)
	}
	if errors.Is(got, compositeapp.ErrBackfillBusy) || errors.Is(got, compositeapp.ErrNothingToAcquire) {
		t.Errorf("err = %v, want it not mistaken for a refusal a Build absorbs", got)
	}
}

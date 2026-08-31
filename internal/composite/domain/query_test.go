package domain_test

import (
	"errors"
	"testing"
	"time"

	acq "github.com/agnos/agnoforge/internal/acquisition/domain"
	"github.com/agnos/agnoforge/internal/composite/domain"
)

// queryable is a built Composite Dataset: declared over January, resolved to
// the end of it, materializing two higher frames.
func queryable() domain.Dataset {
	return domain.Dataset{
		Name: "btc-usd",
		Config: domain.Config{
			Instrument:     "BTC/USD",
			RequestedStart: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			RequestedEnd:   domain.NowEnd(),
			Timeframes:     []domain.Timeframe{domain.TF5m, domain.TF1h},
			Mode:           domain.ModeStrict,
		},
		State:       domain.StateReady,
		ResolvedEnd: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestADatasetServesItsOwnTimelineAndTheFramesItMaterializes: the 1-minute
// timeline is always servable, the declared frames are, and a canonical frame
// the declaration does not name is refused by name.
func TestADatasetServesItsOwnTimelineAndTheFramesItMaterializes(t *testing.T) {
	d := queryable()
	for _, tf := range []domain.Timeframe{domain.TF1m, domain.TF5m, domain.TF1h} {
		if err := d.Serves(tf); err != nil {
			t.Errorf("Serves(%s) = %v, want it served", tf, err)
		}
	}
	for _, tf := range []domain.Timeframe{domain.TF15m, domain.TF1d, domain.TF1w, domain.TF1M} {
		err := d.Serves(tf)
		if !errors.Is(err, domain.ErrTimeframeNotMaterialized) {
			t.Errorf("Serves(%s) = %v, want ErrTimeframeNotMaterialized", tf, err)
		}
	}
	if got := d.ServedTimeframes(); got != "1m, 5m, 1h" {
		t.Errorf("served timeframes = %q, want %q", got, "1m, 5m, 1h")
	}
}

// TestAnUnknownTimeframeIsAMalformedQuery, not a dataset that happens not to
// materialize it: "3m" is not a Timeframe at all.
func TestAnUnknownTimeframeIsAMalformedQuery(t *testing.T) {
	if err := queryable().Serves(domain.Timeframe("3m")); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Errorf("Serves(3m) = %v, want ErrInvalidConfig", err)
	}
}

// TestAnOmittedBoundIsTheDatasetsOwn is the default the backtester contract
// rests on: naming neither bound asks for exactly the dataset's resolved range.
func TestAnOmittedBoundIsTheDatasetsOwn(t *testing.T) {
	d := queryable()
	got, err := d.QueryRange(time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	want := acq.Range{Start: d.Config.RequestedStart, End: d.ResolvedEnd}
	if !got.Start.Equal(want.Start) || !got.End.Equal(want.End) {
		t.Fatalf("range = %s, want the dataset's own %s", got, want)
	}

	// Each bound defaults on its own.
	from := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	got, err = d.QueryRange(from, time.Time{})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if !got.Start.Equal(from) || !got.End.Equal(d.ResolvedEnd) {
		t.Errorf("range = %s, want [%s, resolved end)", got, from)
	}
	to := time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC)
	got, err = d.QueryRange(time.Time{}, to)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if !got.Start.Equal(d.Config.RequestedStart) || !got.End.Equal(to) {
		t.Errorf("range = %s, want [requested start, %s)", got, to)
	}
}

// TestAnEmptyQueryRangeIsRefused: there are no instants in [t, t), so the
// question is malformed rather than answerable with nothing.
func TestAnEmptyQueryRangeIsRefused(t *testing.T) {
	at := time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)
	if _, err := queryable().QueryRange(at, at); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Errorf("QueryRange of an empty range = %v, want ErrInvalidConfig", err)
	}
	if _, err := queryable().QueryRange(at, at.Add(-time.Hour)); !errors.Is(err, domain.ErrInvalidConfig) {
		t.Errorf("QueryRange of a reversed range = %v, want ErrInvalidConfig", err)
	}
}

// TestADatasetWithNoResolvedEndHasNoRangeToDefaultTo: a draft cannot answer an
// omitted end, and says so as the state it is in.
func TestADatasetWithNoResolvedEndHasNoRangeToDefaultTo(t *testing.T) {
	d := queryable()
	d.State, d.ResolvedEnd = domain.StateDraft, time.Time{}
	if _, err := d.QueryRange(time.Time{}, time.Time{}); !errors.Is(err, domain.ErrNotBuilt) {
		t.Errorf("QueryRange of a draft = %v, want ErrNotBuilt", err)
	}
}

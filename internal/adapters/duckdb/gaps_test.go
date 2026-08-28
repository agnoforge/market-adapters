package duckdb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// gap builds a Gap on btcusdt1m between two epoch minutes.
func gap(startMinute, endMinute int64, status domain.GapStatus) domain.Gap {
	return domain.Gap{
		Dataset: btcusdt1m,
		Range:   rng(startMinute, endMinute),
		Status:  status,
		Reason:  "",
	}
}

// statuses renders the stored Gaps as "start-end:status" for comparison.
func summarise(gaps []domain.Gap) []string {
	out := make([]string, len(gaps))
	for i, g := range gaps {
		out[i] = string(g.Status) + "@" + g.Range.String()
	}
	return out
}

func TestReplaceOpenGapsDeletesOnlyOpenGapsInRange(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	// Seed four Gaps by writing each with a range that touches nothing else,
	// so no seeding call deletes an earlier one.
	seed := []domain.Gap{
		gap(0, 10, domain.GapOpen),           // open, inside the replaced range
		gap(20, 30, domain.GapIgnored),       // ignored, inside the replaced range
		gap(40, 50, domain.GapUnrecoverable), // unrecoverable, inside the replaced range
		gap(200, 210, domain.GapOpen),        // open, outside the replaced range
	}
	for _, g := range seed {
		if err := store.ReplaceOpenGaps(ctx, btcusdt1m, domain.Range{}, []domain.Gap{g}); err != nil {
			t.Fatalf("seeding %v: %v", g.Range, err)
		}
	}

	fresh := []domain.Gap{gap(60, 70, domain.GapOpen)}
	if err := store.ReplaceOpenGaps(ctx, btcusdt1m, rng(0, 100), fresh); err != nil {
		t.Fatalf("ReplaceOpenGaps: %v", err)
	}

	got, err := store.Gaps(ctx, btcusdt1m, app.GapFilter{})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	want := []string{
		"ignored@" + rng(20, 30).String(),
		"unrecoverable@" + rng(40, 50).String(),
		"open@" + rng(60, 70).String(),
		"open@" + rng(200, 210).String(),
	}
	gotSummary := summarise(got)
	if len(gotSummary) != len(want) {
		t.Fatalf("Gaps = %v, want %v", gotSummary, want)
	}
	for i := range want {
		if gotSummary[i] != want[i] {
			t.Fatalf("Gaps = %v, want %v", gotSummary, want)
		}
	}
}

func TestReplaceOpenGapsUsesHalfOpenIntersection(t *testing.T) {
	ctx := context.Background()
	store := open(t)

	// [10,20) only touches [0,10) and [20,30): touching is not intersecting,
	// so both survive.
	for _, g := range []domain.Gap{gap(0, 10, domain.GapOpen), gap(20, 30, domain.GapOpen)} {
		if err := store.ReplaceOpenGaps(ctx, btcusdt1m, domain.Range{}, []domain.Gap{g}); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	if err := store.ReplaceOpenGaps(ctx, btcusdt1m, rng(10, 20), nil); err != nil {
		t.Fatalf("ReplaceOpenGaps: %v", err)
	}
	got, err := store.Gaps(ctx, btcusdt1m, app.GapFilter{})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("touching Gaps were deleted: %v", summarise(got))
	}
}

func TestReplaceOpenGapsKeepsDatasetsApart(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	other := domain.DatasetID{Provider: "binance", Symbol: "ETHUSDT", Timeframe: domain.TF1m}

	if err := store.ReplaceOpenGaps(ctx, other, domain.Range{}, []domain.Gap{gap(0, 10, domain.GapOpen)}); err != nil {
		t.Fatalf("seeding other: %v", err)
	}
	if err := store.ReplaceOpenGaps(ctx, btcusdt1m, rng(0, 100), nil); err != nil {
		t.Fatalf("ReplaceOpenGaps: %v", err)
	}
	got, err := store.Gaps(ctx, other, app.GapFilter{})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("another Dataset's open Gap was deleted: %v", summarise(got))
	}
}

func TestGapsFilters(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	seed := []domain.Gap{
		gap(0, 10, domain.GapOpen),
		gap(20, 30, domain.GapIgnored),
		gap(40, 50, domain.GapOpen),
	}
	for _, g := range seed {
		if err := store.ReplaceOpenGaps(ctx, btcusdt1m, domain.Range{}, []domain.Gap{g}); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	openStatus := domain.GapOpen
	middle := rng(25, 45)
	cases := []struct {
		name   string
		filter app.GapFilter
		want   []string
	}{
		{"none", app.GapFilter{}, []string{
			"open@" + rng(0, 10).String(),
			"ignored@" + rng(20, 30).String(),
			"open@" + rng(40, 50).String(),
		}},
		{"status", app.GapFilter{Status: &openStatus}, []string{
			"open@" + rng(0, 10).String(),
			"open@" + rng(40, 50).String(),
		}},
		{"range", app.GapFilter{Range: &middle}, []string{
			"ignored@" + rng(20, 30).String(),
			"open@" + rng(40, 50).String(),
		}},
		{"status and range", app.GapFilter{Status: &openStatus, Range: &middle}, []string{
			"open@" + rng(40, 50).String(),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := store.Gaps(ctx, btcusdt1m, c.filter)
			if err != nil {
				t.Fatalf("Gaps: %v", err)
			}
			gotSummary := summarise(got)
			if len(gotSummary) != len(c.want) {
				t.Fatalf("Gaps = %v, want %v", gotSummary, c.want)
			}
			for i := range c.want {
				if gotSummary[i] != c.want[i] {
					t.Fatalf("Gaps = %v, want %v", gotSummary, c.want)
				}
			}
		})
	}
}

func TestGapRoundTripsAndReportsNotFound(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if err := store.ReplaceOpenGaps(ctx, btcusdt1m, domain.Range{}, []domain.Gap{gap(0, 10, domain.GapOpen)}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	stored, err := store.Gaps(ctx, btcusdt1m, app.GapFilter{})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("got %d Gaps, want 1", len(stored))
	}
	if stored[0].ID == 0 {
		t.Error("the database did not assign a Gap ID")
	}

	one, err := store.Gap(ctx, stored[0].ID)
	if err != nil {
		t.Fatalf("Gap: %v", err)
	}
	if one.Dataset != btcusdt1m {
		t.Errorf("Gap.Dataset = %v, want %v", one.Dataset, btcusdt1m)
	}
	if !one.Range.Start.Equal(at(0)) || !one.Range.End.Equal(at(10)) {
		t.Errorf("Gap.Range = %s, want %s", one.Range, rng(0, 10))
	}
	if one.Status != domain.GapOpen {
		t.Errorf("Gap.Status = %q, want %q", one.Status, domain.GapOpen)
	}

	if _, err := store.Gap(ctx, 99999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Gap of a missing id = %v, want ErrNotFound", err)
	}
}

func TestSetGapStatus(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if err := store.ReplaceOpenGaps(ctx, btcusdt1m, domain.Range{}, []domain.Gap{gap(0, 10, domain.GapOpen)}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	stored, err := store.Gaps(ctx, btcusdt1m, app.GapFilter{})
	if err != nil {
		t.Fatalf("Gaps: %v", err)
	}

	if err := store.SetGapStatus(ctx, stored[0].ID, domain.GapIgnored, "binance maintenance"); err != nil {
		t.Fatalf("SetGapStatus: %v", err)
	}
	one, err := store.Gap(ctx, stored[0].ID)
	if err != nil {
		t.Fatalf("Gap: %v", err)
	}
	if one.Status != domain.GapIgnored {
		t.Errorf("Gap.Status = %q, want %q", one.Status, domain.GapIgnored)
	}
	if one.Reason != "binance maintenance" {
		t.Errorf("Gap.Reason = %q, want %q", one.Reason, "binance maintenance")
	}

	if err := store.SetGapStatus(ctx, 99999, domain.GapIgnored, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetGapStatus of a missing id = %v, want ErrNotFound", err)
	}
	if err := store.SetGapStatus(ctx, stored[0].ID, domain.GapStatus("bogus"), ""); err == nil {
		t.Error("SetGapStatus accepted a status outside the canonical four")
	}
}

package duckdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// rng builds a half-open range between two epoch minutes.
func rng(startMinute, endMinute int64) domain.Range {
	return domain.Range{Start: at(startMinute), End: at(endMinute)}
}

func TestExtendCoverageMergesTouchingAndOverlapping(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		added []domain.Range
		want  []domain.Range
	}{
		{"touching", []domain.Range{rng(0, 10), rng(10, 20)}, []domain.Range{rng(0, 20)}},
		{"overlapping", []domain.Range{rng(0, 10), rng(5, 20)}, []domain.Range{rng(0, 20)}},
		{"contained", []domain.Range{rng(0, 20), rng(5, 10)}, []domain.Range{rng(0, 20)}},
		{"same twice", []domain.Range{rng(0, 10), rng(0, 10)}, []domain.Range{rng(0, 10)}},
		{"disjoint", []domain.Range{rng(0, 10), rng(20, 30)}, []domain.Range{rng(0, 10), rng(20, 30)}},
		{"bridged", []domain.Range{rng(0, 10), rng(20, 30), rng(8, 22)}, []domain.Range{rng(0, 30)}},
		{"out of order", []domain.Range{rng(20, 30), rng(0, 10)}, []domain.Range{rng(0, 10), rng(20, 30)}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := open(t)
			for _, r := range c.added {
				if err := store.ExtendCoverage(ctx, btcusdt1m, r); err != nil {
					t.Fatalf("ExtendCoverage %s: %v", r, err)
				}
			}
			got, err := store.Coverage(ctx, btcusdt1m)
			if err != nil {
				t.Fatalf("Coverage: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("Coverage = %v, want %v", got, c.want)
			}
			for i := range got {
				if !got[i].Start.Equal(c.want[i].Start) || !got[i].End.Equal(c.want[i].End) {
					t.Fatalf("Coverage = %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestCoverageIsEmptyForUnknownDataset(t *testing.T) {
	store := open(t)
	got, err := store.Coverage(context.Background(), btcusdt1m)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Coverage of an untouched Dataset = %v, want none", got)
	}
}

func TestExtendCoverageKeepsDatasetsApart(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	other := domain.DatasetID{Provider: "binance", Symbol: "ETHUSDT", Timeframe: domain.TF1m}

	if err := store.ExtendCoverage(ctx, btcusdt1m, rng(0, 10)); err != nil {
		t.Fatalf("ExtendCoverage: %v", err)
	}
	if err := store.ExtendCoverage(ctx, other, rng(100, 110)); err != nil {
		t.Fatalf("ExtendCoverage other: %v", err)
	}
	got, err := store.Coverage(ctx, btcusdt1m)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if len(got) != 1 || !got[0].End.Equal(at(10)) {
		t.Fatalf("Coverage = %v, want [%s]", got, rng(0, 10))
	}
}

func TestCoverageComesBackUTC(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	r := domain.Range{Start: at(0).In(tokyo), End: at(10).In(tokyo)}
	if err := store.ExtendCoverage(ctx, btcusdt1m, r); err != nil {
		t.Fatalf("ExtendCoverage: %v", err)
	}
	got, err := store.Coverage(ctx, btcusdt1m)
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	if len(got) != 1 || got[0].Start.Location() != time.UTC {
		t.Fatalf("Coverage = %v, want one range in UTC", got)
	}
}

func TestOpenTimesYieldsBarsInRangeOnly(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	bars := []domain.Bar{bar(1, "101.00000000"), bar(2, "102.00000000"), bar(5, "105.00000000"), bar(9, "109.00000000")}
	if err := store.UpsertBars(ctx, btcusdt1m, bars); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}

	var got []int64
	for openTime, err := range store.OpenTimes(ctx, btcusdt1m, rng(2, 9)) {
		if err != nil {
			t.Fatalf("OpenTimes: %v", err)
		}
		if openTime.Location() != time.UTC {
			t.Errorf("open_time %s is not UTC", openTime)
		}
		got = append(got, openTime.UnixMilli()/60_000)
	}
	// Half-open: minute 2 is in, minute 9 is out.
	want := []int64{2, 5}
	if len(got) != len(want) {
		t.Fatalf("OpenTimes = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("OpenTimes = %v, want %v", got, want)
		}
	}
}

func TestOpenTimesStopsEarly(t *testing.T) {
	ctx := context.Background()
	store := open(t)
	if err := store.UpsertBars(ctx, btcusdt1m, []domain.Bar{bar(1, "101.00000000"), bar(2, "102.00000000"), bar(3, "103.00000000")}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	seen := 0
	for range store.OpenTimes(ctx, btcusdt1m, rng(0, 100)) {
		seen++
		break
	}
	if seen != 1 {
		t.Fatalf("breaking out of OpenTimes yielded %d times, want 1", seen)
	}
}

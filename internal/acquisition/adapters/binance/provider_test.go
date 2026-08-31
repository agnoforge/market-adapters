package binance_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/adapters/binance"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

const symbol = domain.Symbol("BTCUSDT")

func TestNameIsBinance(t *testing.T) {
	p := binance.New("http://unused.invalid", nil)
	if got := p.Name(); got != "binance" {
		t.Errorf("Name() = %q, want %q", got, "binance")
	}
}

func TestCalendarIsContinuous(t *testing.T) {
	p := binance.New("http://unused.invalid", nil)
	if _, ok := p.Calendar(symbol).(domain.Continuous); !ok {
		t.Errorf("Calendar() = %T, want domain.Continuous", p.Calendar(symbol))
	}
}

// The Provider offers exactly the canonical fixed-duration timeframes:
// Binance's own 1s, 1w and 1M are deliberately absent.
func TestSupportedTimeframesIsTheCanonicalSet(t *testing.T) {
	want := []domain.Timeframe{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d"}
	got := binance.New("http://unused.invalid", nil).SupportedTimeframes()
	if !slices.Equal(got, want) {
		t.Errorf("SupportedTimeframes() = %v, want %v", got, want)
	}
}

func TestUnsupportedTimeframeIsPermanentAndNeverAsks(t *testing.T) {
	s := serving(t, bars(origin, domain.TF1m, 10))
	p := s.provider(newClock(origin.Add(time.Hour)))

	r := domain.Range{Start: origin, End: origin.Add(time.Hour)}
	for _, tf := range []domain.Timeframe{"1w", "1s", "1M", "banana"} {
		_, pages, err := collect(p.Bars(context.Background(), symbol, tf, r))
		if !errors.Is(err, domain.ErrUnsupportedTimeframe) {
			t.Errorf("Bars(%q) error = %v, want ErrUnsupportedTimeframe", tf, err)
		}
		if len(pages) != 0 {
			t.Errorf("Bars(%q) yielded %d pages, want 0", tf, len(pages))
		}
	}
	if s.requests() != 0 {
		t.Errorf("unsupported timeframe made %d requests, want 0", s.requests())
	}
}

// A 2500-bar range is three pages — 1000, 1000, 500 — and every bar arrives
// exactly once, in order.
func TestBarsPagesAtAThousandAndYieldsEveryBarOnce(t *testing.T) {
	const total = 2500
	fixture := bars(origin, domain.TF1m, total)
	s := serving(t, fixture)

	end := origin.Add(total * time.Minute)
	p := s.provider(newClock(end))

	got, pages, err := collect(p.Bars(context.Background(), symbol, domain.TF1m, domain.Range{Start: origin, End: end}))
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if want := []int{1000, 1000, 500}; !slices.Equal(pages, want) {
		t.Errorf("page sizes = %v, want %v", pages, want)
	}
	if len(got) != total {
		t.Fatalf("got %d bars, want %d", len(got), total)
	}
	for i, b := range got {
		want := origin.Add(time.Duration(i) * time.Minute)
		if !b.OpenTime.Equal(want) {
			t.Fatalf("bar %d open_time = %s, want %s (duplicate or skipped bar)", i, b.OpenTime, want)
		}
	}
	// one EarliestAvailable probe plus three pages
	if s.requests() != 4 {
		t.Errorf("made %d requests, want 4", s.requests())
	}
	s.assertKlinesOnly()
}

// The end is clipped to the last fully closed bar, so the candle still
// forming is never yielded — even when the Provider hands it over anyway.
func TestBarsNeverYieldsTheInProgressCandle(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 10)
	// A server that ignores endTime, so the forming candle is offered.
	s := newStub(t, func(w http.ResponseWriter, q url.Values, _ int) {
		q.Del("endTime")
		writeJSON(w, http.StatusOK, encode(slice(t, fixture, q)))
	})

	inProgress := origin.Add(9*time.Minute + 30*time.Second)
	p := s.provider(newClock(inProgress))

	asked := domain.Range{Start: origin, End: origin.Add(10 * time.Minute)}
	got, _, err := collect(p.Bars(context.Background(), symbol, domain.TF1m, asked))
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("got %d bars, want 9 (the 10th is still forming)", len(got))
	}
	forming := origin.Add(9 * time.Minute)
	for _, b := range got {
		if !b.OpenTime.Before(forming) {
			t.Errorf("yielded bar at %s, which closes after now %s", b.OpenTime, inProgress)
		}
	}

	// Once that bar has closed, it is yielded.
	closed := s.provider(newClock(origin.Add(10 * time.Minute)))
	got, _, err = collect(closed.Bars(context.Background(), symbol, domain.TF1m, asked))
	if err != nil {
		t.Fatalf("Bars after close: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("after the bar closed got %d bars, want 10", len(got))
	}
	s.assertKlinesOnly()
}

// The start is clipped up to EarliestAvailable, so asking for prehistory is
// not an error.
func TestBarsClipsStartToEarliestAvailable(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 5)
	s := serving(t, fixture)
	p := s.provider(newClock(origin.Add(5 * time.Minute)))

	asked := domain.Range{Start: origin.Add(-72 * time.Hour), End: origin.Add(5 * time.Minute)}
	got, _, err := collect(p.Bars(context.Background(), symbol, domain.TF1m, asked))
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d bars, want 5", len(got))
	}
	if !got[0].OpenTime.Equal(origin) {
		t.Errorf("first bar = %s, want the earliest available %s", got[0].OpenTime, origin)
	}
}

func TestEarliestAvailableIsTheFirstFixtureOpenTime(t *testing.T) {
	first := origin.Add(1234 * time.Minute)
	s := serving(t, bars(first, domain.TF1m, 20))
	p := s.provider(newClock(first.Add(time.Hour)))

	got, err := p.EarliestAvailable(context.Background(), symbol)
	if err != nil {
		t.Fatalf("EarliestAvailable: %v", err)
	}
	if !got.Equal(first) {
		t.Errorf("EarliestAvailable() = %s, want %s", got, first)
	}
	if got.Location() != time.UTC {
		t.Errorf("EarliestAvailable() location = %s, want UTC", got.Location())
	}
	if s.requests() != 1 {
		t.Errorf("made %d requests, want 1", s.requests())
	}
	s.assertKlinesOnly()
}

// A bar whose low sits above the body is not a bar. It is dropped and logged,
// never yielded.
func TestInvalidBarIsDroppedAndLogged(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 3)
	fixture[1].low = "200.00000000" // low above min(open, close)

	s := serving(t, fixture)
	log := &capture{}
	p := s.provider(newClock(origin.Add(3*time.Minute)), binance.WithLogger(slog.New(log)))

	got, _, err := collect(p.Bars(context.Background(), symbol, domain.TF1m,
		domain.Range{Start: origin, End: origin.Add(3 * time.Minute)}))
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d bars, want 2 (the invalid one dropped)", len(got))
	}
	for _, b := range got {
		if b.OpenTime.Equal(origin.Add(time.Minute)) {
			t.Errorf("yielded the invalid bar at %s", b.OpenTime)
		}
	}
	records := log.records()
	if len(records) != 1 {
		t.Fatalf("logged %d records, want 1: %v", len(records), records)
	}
	if records[0].Level != slog.LevelWarn {
		t.Errorf("logged at %s, want WARN", records[0].Level)
	}
	if records[0].Message != "binance: dropping invalid bar" {
		t.Errorf("log message = %q", records[0].Message)
	}
}

// capture is a slog.Handler that keeps every record.
type capture struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }
func (c *capture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, r)
	return nil
}
func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capture) WithGroup(string) slog.Handler      { return c }
func (c *capture) records() []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]slog.Record(nil), c.recs...)
}

// The public base URL is unreachable from a test, which is what makes the
// httptest-only rule enforceable rather than a convention.
func TestDefaultBaseURLIsUnreachableFromATest(t *testing.T) {
	c := newClock(origin)
	p := binance.New(binance.DefaultBaseURL, nil, binance.WithClock(c.Now), binance.WithSleep(c.Sleep))

	_, err := p.EarliestAvailable(context.Background(), symbol)
	if err == nil {
		t.Fatal("a request to the public base URL succeeded; tests must never reach the network")
	}
	if !strings.Contains(err.Error(), "test tried to reach the network") {
		t.Errorf("error = %v, want the loopback guard to have blocked it", err)
	}
}

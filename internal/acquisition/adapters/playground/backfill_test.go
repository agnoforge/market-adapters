package playground

import (
	"context"
	"iter"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/agnos/agnoforge/internal/acquisition/adapters/duckdb"
	"github.com/agnos/agnoforge/internal/acquisition/app"
	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// This file is the one place the store is fed by the real use cases rather
// than by spans a test made up, because "a Backfill is running" is a fact
// about internal/app and nothing else can state it. The dependency runs one
// way only: the playground package itself never names app, and these imports
// live in the test binary alone.

// origin is the instant the fake Provider's first Bar opens.
var origin = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// blockingProvider yields two pages and stops before the second until the
// test lets it through, so there is a moment where the Backfill is provably
// mid-flight.
type blockingProvider struct {
	held    chan struct{}
	release chan struct{}
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{held: make(chan struct{}), release: make(chan struct{})}
}

func (p *blockingProvider) Name() string { return "binance" }

func (p *blockingProvider) SupportedTimeframes() []domain.Timeframe {
	return []domain.Timeframe{domain.TF1m}
}

func (p *blockingProvider) EarliestAvailable(context.Context, domain.Symbol) (time.Time, error) {
	return origin, nil
}

func (p *blockingProvider) Calendar(domain.Symbol) domain.TradingCalendar { return domain.Continuous{} }

func (p *blockingProvider) Bars(ctx context.Context, s domain.Symbol, tf domain.Timeframe, r domain.Range) iter.Seq2[[]domain.Bar, error] {
	return func(yield func([]domain.Bar, error) bool) {
		if !yield(bars(origin, 0, 1), nil) {
			return
		}
		close(p.held)
		<-p.release
		yield(bars(origin, 1, 2), nil)
	}
}

// bars builds the Bars opening at offsets [from, to) minutes after start.
func bars(start time.Time, from, to int) []domain.Bar {
	out := make([]domain.Bar, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, domain.Bar{
			OpenTime: start.Add(time.Duration(i) * time.Minute).UTC(),
			Open:     "10", High: "12", Low: "9", Close: "11", Volume: "100",
		})
	}
	return out
}

// A Backfill in flight is visible by its id, with its running spans reporting
// a null end; once it has finished, every span of it has an end.
func TestARunningBackfillIsVisibleByItsID(t *testing.T) {
	store := NewStore()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(store))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		_ = tp.Shutdown(context.Background())
	})

	mux := http.NewServeMux()
	store.Register(mux)
	h := &harness{t: t, store: store, tp: tp, mux: mux}

	db, err := duckdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	provider := newBlockingProvider()
	now := origin.Add(time.Hour)
	svc := app.New(db, []app.Provider{provider},
		app.WithLogger(slog.New(slog.DiscardHandler)),
		app.WithClock(func() time.Time { return now }))

	status, err := svc.StartBackfill(context.Background(), app.BackfillRequest{
		Provider:  "binance",
		Symbol:    domain.Symbol("BTCUSDT"),
		Timeframe: domain.TF1m,
		Range:     domain.Range{Start: origin, End: origin.Add(2 * time.Minute)},
	})
	if err != nil {
		t.Fatalf("start backfill: %v", err)
	}
	backfillID := string(status.ID)

	<-provider.held
	traces := h.backfillTraces(backfillID)
	if len(traces) == 0 {
		t.Fatalf("?backfill_id=%s returned no trace while the Backfill was running", backfillID)
	}
	running := 0
	names := map[string]bool{}
	for _, tr := range traces {
		for _, span := range spansOf(t, tr) {
			names[span["name"].(string)] = true
			if span["end"] == nil {
				running++
			}
		}
	}
	if running == 0 {
		t.Errorf("no span reported end: null while the Backfill was running; spans seen: %v", names)
	}
	if !names["app.HistoricalBackfill"] {
		t.Errorf("the execution trace is missing app.HistoricalBackfill; spans seen: %v", names)
	}

	close(provider.release)
	if _, ok := svc.Wait(status.ID); !ok {
		t.Fatal("Wait reported no such Backfill")
	}

	traces = h.backfillTraces(backfillID)
	if len(traces) == 0 {
		t.Fatalf("?backfill_id=%s returned no trace after Wait", backfillID)
	}
	total := 0
	for _, tr := range traces {
		for _, span := range spansOf(t, tr) {
			total++
			if span["end"] == nil {
				t.Errorf("span %v still has end: null after Wait", span["name"])
			}
		}
	}
	if total == 0 {
		t.Fatal("the finished Backfill has no spans")
	}
}

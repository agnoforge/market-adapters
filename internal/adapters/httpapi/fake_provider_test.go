package httpapi_test

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

// fakeProvider is a Provider entirely under the test's control, so the HTTP
// adapter can be exercised without a network. It is this package's own copy:
// a _test.go file belongs to one package and cannot be shared.
type fakeProvider struct {
	name       string
	timeframes []domain.Timeframe

	// earliest is the open_time EarliestAvailable reports.
	earliest time.Time
	// unknown is the Symbol EarliestAvailable refuses, the way a Provider
	// refuses a Symbol it does not list.
	unknown domain.Symbol

	// pages are yielded in order, one per iteration step.
	pages [][]domain.Bar

	mu sync.Mutex
	// block, when set, holds Bars before its first page. It is how a test
	// keeps one Backfill running while it makes a second request.
	block chan struct{}
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) SupportedTimeframes() []domain.Timeframe {
	out := make([]domain.Timeframe, len(p.timeframes))
	copy(out, p.timeframes)
	return out
}

func (p *fakeProvider) EarliestAvailable(ctx context.Context, s domain.Symbol) (time.Time, error) {
	if p.unknown != "" && s == p.unknown {
		return time.Time{}, domain.ErrUnknownSymbol
	}
	return p.earliest, nil
}

func (p *fakeProvider) Calendar(s domain.Symbol) domain.TradingCalendar { return domain.Continuous{} }

func (p *fakeProvider) Bars(ctx context.Context, s domain.Symbol, tf domain.Timeframe, r domain.Range) iter.Seq2[[]domain.Bar, error] {
	return func(yield func([]domain.Bar, error) bool) {
		p.mu.Lock()
		block := p.block
		p.mu.Unlock()
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
		for _, page := range p.pages {
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			if !yield(page, nil) {
				return
			}
		}
	}
}

// hold makes the next Backfill block before its first page, and returns the
// release. The Backfill stays in state running until it is called, which is
// what a test needs to observe a second request being refused.
func (p *fakeProvider) hold() (release func()) {
	block := make(chan struct{})
	p.mu.Lock()
	p.block = block
	p.mu.Unlock()
	return sync.OnceFunc(func() {
		p.mu.Lock()
		p.block = nil
		p.mu.Unlock()
		close(block)
	})
}

// testBarsAt builds one Bar per offset, offset i opening at start + i*tf. It
// is how a test punches a hole in a page.
func testBarsAt(start time.Time, tf domain.Timeframe, offsets ...int) []domain.Bar {
	out := make([]domain.Bar, 0, len(offsets))
	for _, i := range offsets {
		out = append(out, domain.Bar{
			OpenTime: start.Add(time.Duration(i) * tf.Duration()).UTC(),
			Open:     "10",
			High:     "12",
			Low:      "9",
			Close:    "11",
			Volume:   "100",
		})
	}
	return out
}

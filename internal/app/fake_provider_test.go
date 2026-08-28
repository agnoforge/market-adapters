package app_test

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/agnos/agnoforge/internal/domain"
)

// fakePage is one page a fakeProvider yields: bars, or the failure that ends
// the sequence.
type fakePage struct {
	bars []domain.Bar
	err  error
}

// fakeProvider is a Provider entirely under the test's control. It exists to
// prove the acceptance criterion that a second Provider needs no change under
// internal/app: it implements the port and nothing else.
type fakeProvider struct {
	name       string
	timeframes []domain.Timeframe

	// earliest is the open_time EarliestAvailable reports.
	earliest time.Time
	// earliestErr, when set, is consulted on every EarliestAvailable call
	// with the 1-based call count; a non-nil result is returned instead of
	// earliest.
	earliestErr func(call int) error

	// calendar is the TradingCalendar Calendar reports. A nil calendar means
	// the Provider is continuous, like Binance.
	calendar domain.TradingCalendar

	// pages are yielded in order, one per iteration step.
	pages []fakePage
	// onPage, when set, runs before page i is yielded. It is the seam a test
	// uses to inspect the Store between pages, or to block one Backfill while
	// another runs.
	onPage func(i int)

	mu        sync.Mutex
	calls     int
	barsRange domain.Range
	symbol    domain.Symbol
	timeframe domain.Timeframe
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) SupportedTimeframes() []domain.Timeframe {
	out := make([]domain.Timeframe, len(p.timeframes))
	copy(out, p.timeframes)
	return out
}

func (p *fakeProvider) EarliestAvailable(ctx context.Context, s domain.Symbol) (time.Time, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if p.earliestErr != nil {
		if err := p.earliestErr(call); err != nil {
			return time.Time{}, err
		}
	}
	return p.earliest, nil
}

func (p *fakeProvider) Calendar(s domain.Symbol) domain.TradingCalendar {
	if p.calendar != nil {
		return p.calendar
	}
	return domain.Continuous{}
}

func (p *fakeProvider) Bars(ctx context.Context, s domain.Symbol, tf domain.Timeframe, r domain.Range) iter.Seq2[[]domain.Bar, error] {
	return func(yield func([]domain.Bar, error) bool) {
		p.mu.Lock()
		p.barsRange, p.symbol, p.timeframe = r, s, tf
		p.mu.Unlock()

		for i, pg := range p.pages {
			if p.onPage != nil {
				p.onPage(i)
			}
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}
			if !yield(pg.bars, pg.err) || pg.err != nil {
				return
			}
		}
	}
}

// requestedRange reports the Range the use case asked Bars for.
func (p *fakeProvider) requestedRange() domain.Range {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.barsRange
}

// earliestCalls reports how often EarliestAvailable was called.
func (p *fakeProvider) earliestCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// testBars builds n consecutive valid Bars starting at start, one per tf.
func testBars(start time.Time, tf domain.Timeframe, n int) []domain.Bar {
	return testBarsAt(start, tf, seq(0, n)...)
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

// seq returns the n integers starting at from.
func seq(from, n int) []int {
	out := make([]int, 0, n)
	for i := range n {
		out = append(out, from+i)
	}
	return out
}

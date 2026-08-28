package binance

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/app"
	"github.com/agnos/agnoforge/internal/domain"
)

// Provider satisfies the port the use cases depend on.
var _ app.Provider = (*Provider)(nil)

// name is this Provider's identifier, the first component of a DatasetID.
const name = "binance"

// DefaultBaseURL is the public REST endpoint. Nothing in this package
// hardcodes it into a request: the base URL is always injected, so tests can
// point the adapter at an httptest server and never reach the network.
const DefaultBaseURL = "https://api.binance.com"

// baseURLEnv names the environment variable that overrides DefaultBaseURL.
const baseURLEnv = "BINANCE_BASE_URL"

// Rate limit: the Provider publishes a 6000 weight per minute budget, and one
// klines request costs klinesWeight against it.
const (
	budgetWeight = 6000
	budgetWindow = time.Minute
)

// pageLimit is the most bars one klines request returns.
const pageLimit = 1000

// defaultBackoff is the first retry delay; each further retry doubles it.
const defaultBackoff = 500 * time.Millisecond

// BaseURL returns the base URL to build a Provider against: BINANCE_BASE_URL
// when it is set, otherwise DefaultBaseURL.
func BaseURL() string {
	if v := os.Getenv(baseURLEnv); v != "" {
		return v
	}
	return DefaultBaseURL
}

// Provider is the Binance implementation of the app.Provider port. Build one
// and share it: its rate-limit budget is held per Provider, so every
// concurrent Bars caller draws from the same bucket.
type Provider struct {
	baseURL string
	client  *http.Client
	bucket  *bucket
	log     *slog.Logger

	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
	backoffBase time.Duration
}

// An Option adjusts a Provider at construction. The seams exist so tests can
// run on a fake clock with no real sleeping and capture what was logged.
type Option func(*Provider)

// WithClock replaces the source of the current time, which decides where the
// last fully closed Bar is and drives the rate-limit budget.
func WithClock(now func() time.Time) Option {
	return func(p *Provider) {
		if now != nil {
			p.now = now
		}
	}
}

// WithSleep replaces the way the adapter waits, for both retry backoff and
// the rate-limit budget.
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(p *Provider) {
		if sleep != nil {
			p.sleep = sleep
		}
	}
}

// WithLogger replaces the logger that records dropped bars.
func WithLogger(log *slog.Logger) Option {
	return func(p *Provider) {
		if log != nil {
			p.log = log
		}
	}
}

// WithBackoffBase replaces the first retry delay. Each further retry doubles
// whatever this is.
func WithBackoffBase(d time.Duration) Option {
	return func(p *Provider) {
		if d > 0 {
			p.backoffBase = d
		}
	}
}

// New builds a Provider against baseURL. A nil client gets a modest default.
func New(baseURL string, client *http.Client, opts ...Option) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	p := &Provider{
		baseURL:     strings.TrimRight(baseURL, "/"),
		client:      client,
		log:         slog.Default(),
		now:         time.Now,
		sleep:       sleepContext,
		backoffBase: defaultBackoff,
	}
	for _, opt := range opts {
		opt(p)
	}
	p.bucket = newBucket(budgetWeight, budgetWindow, p.now, p.sleep)
	return p
}

// sleepContext waits for d, or until ctx ends.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Name returns the Provider's identifier.
func (p *Provider) Name() string { return name }

// SupportedTimeframes returns the Timeframes Binance serves: every canonical
// fixed-duration Timeframe. Binance's own 1s, 1w and 1M are deliberately
// absent — a variable-length bar cannot be aligned arithmetically, and the
// domain has no Timeframe for them.
func (p *Provider) SupportedTimeframes() []domain.Timeframe { return domain.Timeframes() }

// Calendar reports that Binance trades continuously: every Timeframe
// boundary in a range is an expected open_time.
func (p *Provider) Calendar(domain.Symbol) domain.TradingCalendar { return domain.Continuous{} }

// EarliestAvailable reports the open_time of the first Bar Binance holds for
// s, by asking for the single oldest bar. An unknown Symbol comes back as an
// error wrapping domain.ErrUnknownSymbol, and is not retried.
func (p *Provider) EarliestAvailable(ctx context.Context, s domain.Symbol) (time.Time, error) {
	ctx, span := tracer().Start(ctx, "binance.EarliestAvailable",
		trace.WithAttributes(append(providerAttrs(), symbolKey.String(string(s)))...))
	defer span.End()

	rows, err := p.klines(ctx, url.Values{
		"symbol":    {string(s)},
		"interval":  {string(domain.TF1m)},
		"startTime": {"0"},
		"limit":     {"1"},
	})
	if err != nil {
		return time.Time{}, fail(span, err)
	}
	if len(rows) == 0 {
		return time.Time{}, fail(span, fmt.Errorf("binance: no bars for symbol %q: %w", s, domain.ErrUnknownSymbol))
	}
	return time.UnixMilli(rows[0].openTime).UTC(), nil
}

// Bars yields the Bars of r one page at a time, in ascending open_time order
// with nothing repeated or skipped.
//
// The range it actually walks is r clipped on both ends: the start rises to
// EarliestAvailable, and the end falls to the open_time of the currently
// forming bar, so a Bar that has not fully closed is never yielded. Bars that
// fail domain.Bar.Validate are dropped with a warning rather than yielded.
func (p *Provider) Bars(ctx context.Context, s domain.Symbol, tf domain.Timeframe, r domain.Range) iter.Seq2[[]domain.Bar, error] {
	return func(yield func([]domain.Bar, error) bool) {
		// The span opens when iteration begins and closes when the sequence
		// is done, so its duration is the whole walk, pages and all.
		ctx, span := tracer().Start(ctx, "binance.Bars", trace.WithAttributes(append(
			providerAttrs(),
			symbolKey.String(string(s)),
			timeframeKey.String(string(tf)),
			rangeStartKey.String(instant(r.Start)),
			rangeEndKey.String(instant(r.End)),
		)...))
		defer span.End()

		if !tf.Valid() {
			yield(nil, fail(span, fmt.Errorf("binance: %w: %q", domain.ErrUnsupportedTimeframe, tf)))
			return
		}
		step := tf.Duration()

		earliest, err := p.EarliestAvailable(ctx, s)
		if err != nil {
			yield(nil, fail(span, err))
			return
		}

		now := p.now().UTC()
		start := r.Start.UTC()
		if start.Before(earliest) {
			start = earliest
		}
		end := r.End.UTC()
		if closed := floorTo(now, tf); closed.Before(end) {
			end = closed
		}
		if !start.Before(end) {
			return
		}
		nowMS := now.UnixMilli()

		for cur := start; cur.Before(end); {
			rows, err := p.page(ctx, s, tf, cur, end)
			if err != nil {
				yield(nil, fail(span, err))
				return
			}
			if len(rows) == 0 {
				return
			}

			page := make([]domain.Bar, 0, len(rows))
			last := cur
			for _, k := range rows {
				b := k.bar()
				if b.OpenTime.After(last) {
					last = b.OpenTime
				}
				if b.OpenTime.Before(start) || !b.OpenTime.Before(end) {
					continue
				}
				if k.closeTime >= nowMS {
					// Belt and braces: the end is already clipped to the
					// last closed bar, but a bar still forming never leaves
					// this adapter.
					continue
				}
				if err := b.Validate(); err != nil {
					p.log.Warn("binance: dropping invalid bar",
						"provider", name,
						"symbol", string(s),
						"timeframe", string(tf),
						"open_time", b.OpenTime.Format(time.RFC3339Nano),
						"error", err)
					continue
				}
				page = append(page, b)
			}
			if len(page) > 0 && !yield(page, nil) {
				return
			}
			if len(rows) < pageLimit {
				return
			}
			next := last.Add(step)
			if !next.After(cur) {
				// The Provider did not move; stop rather than loop forever.
				return
			}
			cur = next
		}
	}
}

// page fetches one page of at most pageLimit bars starting at cur. Binance's
// endTime is inclusive while a Range is half-open, so the bound is pulled
// back by a millisecond.
func (p *Provider) page(ctx context.Context, s domain.Symbol, tf domain.Timeframe, cur, end time.Time) ([]kline, error) {
	return p.klines(ctx, url.Values{
		"symbol":    {string(s)},
		"interval":  {string(tf)},
		"startTime": {strconv.FormatInt(cur.UnixMilli(), 10)},
		"endTime":   {strconv.FormatInt(end.UnixMilli()-1, 10)},
		"limit":     {strconv.Itoa(pageLimit)},
	})
}

// klines is one page fetch: the request, its retries, and the decoding of what
// came back. It is the span a caller sees per fetch, which is why the bar
// count belongs on it — a page is only a page once it is decoded.
func (p *Provider) klines(ctx context.Context, params url.Values) ([]kline, error) {
	ctx, span := tracer().Start(ctx, "binance.get", trace.WithAttributes(providerAttrs()...))
	defer span.End()

	body, err := p.get(ctx, params)
	if err != nil {
		return nil, fail(span, err)
	}
	rows, err := decodeKlines(body)
	if err != nil {
		return nil, fail(span, err)
	}
	span.SetAttributes(pageBarCountKey.Int(len(rows)))
	return rows, nil
}

// instant is how a range bound reaches a span: RFC3339, in UTC.
func instant(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// floorTo rounds t down to the Timeframe boundary at or before it, measuring
// boundaries from the Unix epoch. That instant is the open_time of the bar
// still forming, and so the exclusive end of the fully closed ones.
func floorTo(t time.Time, tf domain.Timeframe) time.Time {
	step := tf.Duration().Milliseconds()
	if step <= 0 {
		return t
	}
	ms := t.UnixMilli()
	rem := ((ms % step) + step) % step
	return time.UnixMilli(ms - rem).UTC()
}

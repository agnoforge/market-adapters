package binance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/adapters/binance"
	"github.com/agnos/agnoforge/internal/domain"
)

// origin is the instant every fixture starts at.
var origin = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// clock is a fake clock. It only ever moves when something sleeps on it,
// which makes both the retry backoff and the rate-limit budget observable
// without a test ever waiting.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newClock(at time.Time) *clock { return &clock{now: at} }

// Now reports the fake current time.
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep records the wait and advances the fake clock by it.
func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

// Sleeps returns every delay waited so far, in order.
func (c *clock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// row is one fixture bar, in the wire shape Binance sends.
type row struct {
	openTime  int64
	closeTime int64
	open      string
	high      string
	low       string
	closePx   string
	volume    string
}

// bars builds n consecutive fixture rows of timeframe tf beginning at start.
func bars(start time.Time, tf domain.Timeframe, n int) []row {
	step := tf.Duration()
	out := make([]row, 0, n)
	for i := range n {
		open := start.Add(time.Duration(i) * step)
		out = append(out, row{
			openTime:  open.UnixMilli(),
			closeTime: open.Add(step).UnixMilli() - 1,
			open:      "100.00000000",
			high:      "101.00000000",
			low:       "99.00000000",
			closePx:   "100.50000000",
			volume:    "1.00000000",
		})
	}
	return out
}

// encode renders rows the way the REST endpoint does: an array of
// heterogeneous arrays, numbers for the times and strings for the prices.
func encode(rows []row) []byte {
	out := make([][]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, []any{
			r.openTime, r.open, r.high, r.low, r.closePx, r.volume, r.closeTime,
			"0.00000000", 0, "0.00000000", "0.00000000", "0",
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	return b
}

// stub is a fake Binance that records every request it received.
type stub struct {
	t   *testing.T
	srv *httptest.Server

	mu    sync.Mutex
	paths []string
	calls int
}

// newStub starts a fake Binance whose responses respond decides. respond is
// given the query and the 1-based number of the request.
func newStub(t *testing.T, respond func(w http.ResponseWriter, q url.Values, call int)) *stub {
	t.Helper()
	s := &stub{t: t}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls++
		call := s.calls
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		respond(w, r.URL.Query(), call)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// serving starts a fake Binance that answers every request from rows,
// honouring startTime, endTime and limit the way the real endpoint does.
func serving(t *testing.T, rows []row) *stub {
	t.Helper()
	return newStub(t, func(w http.ResponseWriter, q url.Values, _ int) {
		writeJSON(w, http.StatusOK, encode(slice(t, rows, q)))
	})
}

// slice applies startTime, endTime and limit to the fixture.
func slice(t *testing.T, rows []row, q url.Values) []row {
	t.Helper()
	start := number(t, q, "startTime", 0)
	end := number(t, q, "endTime", 1<<62)
	limit := int(number(t, q, "limit", 500))

	var out []row
	for _, r := range rows {
		if r.openTime < start || r.openTime > end {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out
}

// number reads an integer query parameter, falling back to def when absent.
func number(t *testing.T, q url.Values, key string, def int64) int64 {
	t.Helper()
	v := q.Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		t.Fatalf("query %s=%q is not a number: %v", key, v, err)
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// requests reports how many requests the stub has received.
func (s *stub) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// assertKlinesOnly fails the test unless every request went to the klines
// endpoint, which also proves nothing else was called.
func (s *stub) assertKlinesOnly() {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.paths) == 0 {
		return
	}
	for _, p := range s.paths {
		if p != "/api/v3/klines" {
			s.t.Errorf("request went to %q, want %q", p, "/api/v3/klines")
		}
	}
}

// guard is a RoundTripper that refuses any host but the stub's, so a test
// cannot silently reach the real Binance even if the base URL were wrong.
type guard struct {
	t    *testing.T
	host string
	next http.RoundTripper
}

func (g *guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != g.host {
		g.t.Errorf("request left the test: host %q, want %q", r.URL.Host, g.host)
		return nil, fmt.Errorf("blocked request to %q", r.URL.Host)
	}
	return g.next.RoundTrip(r)
}

// provider builds a Provider pointed at the stub through BINANCE_BASE_URL,
// on the given fake clock, with a transport that cannot leave the test.
func (s *stub) provider(c *clock, opts ...binance.Option) *binance.Provider {
	s.t.Helper()
	s.t.Setenv("BINANCE_BASE_URL", s.srv.URL)

	base := binance.BaseURL()
	if base != s.srv.URL {
		s.t.Fatalf("BaseURL() = %q, want the httptest server %q", base, s.srv.URL)
	}
	host, err := url.Parse(base)
	if err != nil {
		s.t.Fatalf("parse base URL: %v", err)
	}
	client := &http.Client{Transport: &guard{t: s.t, host: host.Host, next: http.DefaultTransport}}

	opts = append([]binance.Option{
		binance.WithClock(c.Now),
		binance.WithSleep(c.Sleep),
	}, opts...)
	return binance.New(base, client, opts...)
}

// collect drains a Bars sequence into every Bar it yielded, the number of
// pages, and the error it ended with.
func collect(seq func(func([]domain.Bar, error) bool)) (all []domain.Bar, pages []int, err error) {
	for page, e := range seq {
		if e != nil {
			err = e
			continue
		}
		pages = append(pages, len(page))
		all = append(all, page...)
	}
	return all, pages, err
}

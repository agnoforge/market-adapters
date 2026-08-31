package binance_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// A rate limit is waited out for exactly as long as the Provider asked, not
// for the adapter's own backoff.
func TestRetryAfterIsHonouredOn429(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 5)
	s := newStub(t, func(w http.ResponseWriter, q url.Values, call int) {
		if call == 1 {
			w.Header().Set("Retry-After", "3")
			writeJSON(w, http.StatusTooManyRequests, []byte(`{"code":-1003,"msg":"Too many requests."}`))
			return
		}
		writeJSON(w, http.StatusOK, encode(slice(t, fixture, q)))
	})

	c := newClock(origin.Add(time.Hour))
	p := s.provider(c)

	got, err := p.EarliestAvailable(context.Background(), symbol)
	if err != nil {
		t.Fatalf("EarliestAvailable after a rate limit: %v", err)
	}
	if !got.Equal(origin) {
		t.Errorf("EarliestAvailable() = %s, want %s", got, origin)
	}
	if want := []time.Duration{3 * time.Second}; !slices.Equal(c.Sleeps(), want) {
		t.Errorf("slept %v, want %v", c.Sleeps(), want)
	}
	if s.requests() != 2 {
		t.Errorf("made %d requests, want 2", s.requests())
	}
	s.assertKlinesOnly()
}

// 418 is the Provider's ban after an ignored 429; it carries the same
// instruction and is retried the same way.
func TestRetryAfterIsHonouredOn418(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 5)
	s := newStub(t, func(w http.ResponseWriter, q url.Values, call int) {
		if call == 1 {
			w.Header().Set("Retry-After", "7")
			writeJSON(w, http.StatusTeapot, []byte(`{"code":-1003,"msg":"IP banned."}`))
			return
		}
		writeJSON(w, http.StatusOK, encode(slice(t, fixture, q)))
	})

	c := newClock(origin.Add(time.Hour))
	if _, err := s.provider(c).EarliestAvailable(context.Background(), symbol); err != nil {
		t.Fatalf("EarliestAvailable after a ban: %v", err)
	}
	if want := []time.Duration{7 * time.Second}; !slices.Equal(c.Sleeps(), want) {
		t.Errorf("slept %v, want %v", c.Sleeps(), want)
	}
}

// Five failures is the end of it: the last error is reported once, after four
// doubling backoffs.
func TestFiveFailuresYieldTheLastError(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ url.Values, _ int) {
		writeJSON(w, http.StatusInternalServerError, []byte(`{"code":-1000,"msg":"Internal error."}`))
	})

	c := newClock(origin.Add(time.Hour))
	p := s.provider(c)

	pages := 0
	var errs []error
	for page, err := range p.Bars(context.Background(), symbol, domain.TF1m,
		domain.Range{Start: origin, End: origin.Add(time.Hour)}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		_ = page
		pages++
	}
	if len(errs) != 1 {
		t.Fatalf("yielded %d errors, want exactly 1: %v", len(errs), errs)
	}
	if pages != 0 {
		t.Errorf("yielded %d pages, want 0", pages)
	}
	if s.requests() != 5 {
		t.Errorf("made %d requests, want 5", s.requests())
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}
	if !slices.Equal(c.Sleeps(), want) {
		t.Errorf("backoff = %v, want %v", c.Sleeps(), want)
	}
	s.assertKlinesOnly()
}

// A transient 5xx is retried and the run continues.
func TestServerErrorIsRetriedThenSucceeds(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 5)
	s := newStub(t, func(w http.ResponseWriter, q url.Values, call int) {
		if call <= 2 {
			writeJSON(w, http.StatusBadGateway, []byte(`bad gateway`))
			return
		}
		writeJSON(w, http.StatusOK, encode(slice(t, fixture, q)))
	})

	c := newClock(origin.Add(time.Hour))
	got, _, err := collect(s.provider(c).Bars(context.Background(), symbol, domain.TF1m,
		domain.Range{Start: origin, End: origin.Add(5 * time.Minute)}))
	if err != nil {
		t.Fatalf("Bars: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d bars, want 5", len(got))
	}
	if want := []time.Duration{500 * time.Millisecond, time.Second}; !slices.Equal(c.Sleeps(), want) {
		t.Errorf("backoff = %v, want %v", c.Sleeps(), want)
	}
}

// An unknown Symbol is permanent: it is reported as the domain sentinel and
// asked exactly once.
func TestUnknownSymbolIsPermanentAndNotRetried(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ url.Values, _ int) {
		writeJSON(w, http.StatusBadRequest, []byte(`{"code":-1121,"msg":"Invalid symbol."}`))
	})

	c := newClock(origin.Add(time.Hour))
	p := s.provider(c)

	_, err := p.EarliestAvailable(context.Background(), domain.Symbol("NOPENOPE"))
	if !errors.Is(err, domain.ErrUnknownSymbol) {
		t.Fatalf("EarliestAvailable error = %v, want ErrUnknownSymbol", err)
	}
	if s.requests() != 1 {
		t.Fatalf("made %d requests, want 1 (permanent errors are not retried)", s.requests())
	}
	if len(c.Sleeps()) != 0 {
		t.Errorf("slept %v, want no waiting", c.Sleeps())
	}

	// The same verdict reaches a caller of Bars.
	_, pages, err := collect(p.Bars(context.Background(), domain.Symbol("NOPENOPE"), domain.TF1m,
		domain.Range{Start: origin, End: origin.Add(time.Hour)}))
	if !errors.Is(err, domain.ErrUnknownSymbol) {
		t.Fatalf("Bars error = %v, want ErrUnknownSymbol", err)
	}
	if len(pages) != 0 {
		t.Errorf("yielded %d pages, want 0", len(pages))
	}
	if s.requests() != 2 {
		t.Errorf("made %d requests in total, want 2 (one each, neither retried)", s.requests())
	}
	s.assertKlinesOnly()
}

// A 4xx that is not a rate limit is permanent too.
func TestOtherClientErrorsArePermanent(t *testing.T) {
	s := newStub(t, func(w http.ResponseWriter, _ url.Values, _ int) {
		writeJSON(w, http.StatusBadRequest, []byte(`{"code":-1100,"msg":"Illegal characters."}`))
	})
	c := newClock(origin.Add(time.Hour))

	if _, err := s.provider(c).EarliestAvailable(context.Background(), symbol); err == nil {
		t.Fatal("EarliestAvailable succeeded, want an error")
	}
	if s.requests() != 1 {
		t.Errorf("made %d requests, want 1", s.requests())
	}
}

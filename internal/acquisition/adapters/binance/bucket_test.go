package binance_test

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/agnos/agnoforge/internal/acquisition/domain"
)

// The Provider's budget is 6000 weight a minute and a klines request costs 2,
// so 3000 requests fit and the 3001st has to wait for the bucket to refill —
// and that budget is one bucket per Provider, shared by every concurrent
// caller. If each caller had its own, the requests below would never wait.
func TestTokenBucketIsSharedAcrossConcurrentCallers(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 1)
	s := newStub(t, func(w http.ResponseWriter, q url.Values, _ int) {
		writeJSON(w, http.StatusOK, encode(slice(t, fixture, q)))
	})

	// The fake clock stands still except when something sleeps on it, so the
	// bucket refills only through a wait a test can see.
	c := newClock(origin.Add(time.Hour))
	p := s.provider(c)
	ctx := context.Background()

	const (
		callers  = 4
		eachCall = 750 // 4 × 750 × weight 2 = 6000, exactly the budget
	)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range eachCall {
				if _, err := p.EarliestAvailable(ctx, symbol); err != nil {
					t.Errorf("EarliestAvailable: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if n := s.requests(); n != callers*eachCall {
		t.Fatalf("made %d requests, want %d", n, callers*eachCall)
	}
	if got := c.Sleeps(); len(got) != 0 {
		t.Fatalf("the first %d requests waited %v, want no waiting inside the budget", callers*eachCall, got)
	}

	// The 3001st request exhausts the budget and must wait for the refill:
	// 2 weight at 6000 per minute is 20ms.
	if _, err := p.EarliestAvailable(ctx, symbol); err != nil {
		t.Fatalf("EarliestAvailable past the budget: %v", err)
	}
	got := c.Sleeps()
	if len(got) != 1 {
		t.Fatalf("request %d waited %v, want exactly one wait", callers*eachCall+1, got)
	}
	if got[0] != 20*time.Millisecond {
		t.Errorf("waited %v for 2 weight at 6000/min, want 20ms", got[0])
	}
	if n := s.requests(); n != callers*eachCall+1 {
		t.Errorf("made %d requests, want %d", n, callers*eachCall+1)
	}
	s.assertKlinesOnly()
}

// The bucket refills continuously: a wait long enough to earn the weight back
// lets the next request through without waiting again.
func TestTokenBucketRefillsContinuously(t *testing.T) {
	fixture := bars(origin, domain.TF1m, 1)
	s := newStub(t, func(w http.ResponseWriter, q url.Values, _ int) {
		writeJSON(w, http.StatusOK, encode(slice(t, fixture, q)))
	})
	c := newClock(origin.Add(time.Hour))
	p := s.provider(c)
	ctx := context.Background()

	for range 3000 {
		if _, err := p.EarliestAvailable(ctx, symbol); err != nil {
			t.Fatalf("EarliestAvailable: %v", err)
		}
	}
	// Half a minute of standing still is worth half the budget.
	c.Sleep(ctx, 30*time.Second)
	before := len(c.Sleeps())
	for range 1500 {
		if _, err := p.EarliestAvailable(ctx, symbol); err != nil {
			t.Fatalf("EarliestAvailable after refill: %v", err)
		}
	}
	if got := c.Sleeps()[before:]; len(got) != 0 {
		t.Errorf("waited %v after a 30s refill, want no waiting for 3000 weight", got)
	}
}

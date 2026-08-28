package binance

import (
	"context"
	"sync"
	"time"
)

// bucket is a continuously refilling token bucket. One bucket lives on each
// *Provider and every concurrent caller of Bars draws from it, which is what
// keeps a fleet of parallel Backfills inside the Provider's published budget
// (ADR-0001: the rate limiter lives in the adapter, not in a scheduler).
//
// It is deliberately a mutex and two floats: the budget is per-process, so
// there is nothing here a library would do better.
type bucket struct {
	mu       sync.Mutex
	capacity float64 // maximum weight held at once
	perSec   float64 // refill rate, weight per second
	tokens   float64 // weight available at last
	last     time.Time

	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// newBucket returns a full bucket that refills capacity weight every window.
func newBucket(capacity float64, window time.Duration, now func() time.Time, sleep func(context.Context, time.Duration) error) *bucket {
	return &bucket{
		capacity: capacity,
		perSec:   capacity / window.Seconds(),
		tokens:   capacity,
		last:     now(),
		now:      now,
		sleep:    sleep,
	}
}

// take blocks until weight is available and then spends it. It returns the
// context's error if the wait is cut short.
func (b *bucket) take(ctx context.Context, weight float64) error {
	for {
		wait, ok := b.reserve(weight)
		if ok {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// reserve spends weight when the bucket holds it, reporting how long a caller
// must wait when it does not.
func (b *bucket) reserve(weight float64) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * b.perSec
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
	if b.tokens >= weight {
		b.tokens -= weight
		return 0, true
	}
	wait := time.Duration((weight - b.tokens) / b.perSec * float64(time.Second))
	if wait <= 0 {
		wait = time.Millisecond
	}
	return wait, false
}

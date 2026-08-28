package binance

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/agnos/agnoforge/internal/domain"
)

// maxAttempts is how many times one request is issued before its last failure
// is reported. Attempts are spaced by exponential backoff, except when the
// Provider states a delay itself.
const maxAttempts = 5

// klinesWeight is what one klines request costs against the Provider's budget.
const klinesWeight = 2

// permanent marks an error retrying cannot fix: an unknown Symbol, a
// malformed request, anything the Provider answered with a 4xx other than a
// rate limit. It unwraps, so errors.Is still sees the domain sentinel inside.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// get issues a klines request, retrying up to maxAttempts times. It spends
// the Provider's rate-limit budget once per attempt, backs off exponentially
// on a transport error or a 5xx, and honours Retry-After on 429 and 418
// instead of backing off. A permanent failure returns immediately.
//
// Every re-attempt is an event on the fetch's span rather than a span of its
// own: a retry is something that happened to one page fetch, not a second one.
func (p *Provider) get(ctx context.Context, params url.Values) ([]byte, error) {
	span := trace.SpanFromContext(ctx)
	backoff := p.backoffBase
	var last error
	for attempt := 1; ; attempt++ {
		if err := p.bucket.take(ctx, klinesWeight); err != nil {
			return nil, fmt.Errorf("binance: waiting for rate limit: %w", err)
		}
		body, retryAfter, err := p.roundTrip(ctx, params)
		if err == nil {
			return body, nil
		}
		last = err
		var perm permanent
		if errors.As(err, &perm) {
			return nil, err
		}
		if attempt >= maxAttempts {
			return nil, fmt.Errorf("binance: %d attempts failed: %w", maxAttempts, last)
		}
		delay := backoff
		if retryAfter > 0 {
			delay = retryAfter
		} else {
			backoff *= 2
		}
		span.AddEvent("retry", trace.WithAttributes(
			retryAttemptKey.Int(attempt+1),
			retryDelayKey.Int64(delay.Milliseconds())))
		if err := p.sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
}

// roundTrip performs one attempt. It reports the delay the Provider asked for
// when it answered with a rate limit, and classifies the failure.
func (p *Provider) roundTrip(ctx context.Context, params url.Values) ([]byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+klinesPath+"?"+params.Encode(), nil)
	if err != nil {
		return nil, 0, permanent{fmt.Errorf("binance: build request: %w", err)}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("binance: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("binance: read response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, 0, nil

	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusTeapot:
		// 418 is Binance's "you kept knocking after a 429" ban.
		return nil, retryAfter(resp.Header.Get("Retry-After")),
			fmt.Errorf("binance: rate limited: status %d: %s", resp.StatusCode, snippet(body))

	case resp.StatusCode >= 500:
		return nil, 0, fmt.Errorf("binance: status %d: %s", resp.StatusCode, snippet(body))

	default:
		if e, ok := decodeAPIError(body); ok && e.Code == codeInvalidSymbol {
			return nil, 0, permanent{fmt.Errorf("binance: %w: %s (code %d)", domain.ErrUnknownSymbol, e.Msg, e.Code)}
		}
		return nil, 0, permanent{fmt.Errorf("binance: status %d: %s", resp.StatusCode, snippet(body))}
	}
}

// retryAfter reads the header as a whole number of seconds. Anything else is
// no instruction at all, and the caller falls back to its backoff.
func retryAfter(header string) time.Duration {
	secs, err := strconv.Atoi(header)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// snippet keeps an error message readable when the body is a whole page.
func snippet(body []byte) string {
	const max = 200
	if len(body) > max {
		return string(body[:max]) + "…"
	}
	return string(body)
}

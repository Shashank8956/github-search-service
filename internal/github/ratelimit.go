package github

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// GitHub's code search budget. It has its own bucket, separate from the
// 5000/hour core limit and the 30/minute search limit.
const defaultRateLimit = 10

// limiter is an interface so tests can drop in one that never waits.
type limiter interface {
	Wait(ctx context.Context) error
	Observe(h http.Header)
}

type rateLimiter struct {
	mu     sync.Mutex
	lim    *rate.Limiter
	perMin int
}

func newRateLimiter(perMin int) *rateLimiter {
	if perMin < 1 {
		perMin = 1
	}
	// Burst equals the budget: GitHub uses a fixed window, not a token bucket.
	return &rateLimiter{
		lim:    rate.NewLimiter(perMinute(perMin), perMin),
		perMin: perMin,
	}
}

func perMinute(n int) rate.Limit {
	return rate.Limit(float64(n) / 60)
}

// Wait blocks until there is budget for one request. It reserves rather than
// calling rate.Limiter.Wait, which consumes the slot even when the caller is
// about to time out.
func (r *rateLimiter) Wait(ctx context.Context) error {
	r.mu.Lock()
	lim := r.lim
	r.mu.Unlock()

	res := lim.Reserve()
	if !res.OK() {
		return &RateLimitError{}
	}

	delay := res.Delay()
	if delay == 0 {
		return nil
	}

	// Caller cannot wait that long, so hand the slot back and say so now.
	if deadline, ok := ctx.Deadline(); ok && time.Now().Add(delay).After(deadline) {
		res.Cancel()
		return &RateLimitError{Reset: time.Now().Add(delay)}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		res.Cancel()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Observe retunes pacing from the response, so a change on GitHub's side is
// picked up without a code change here.
func (r *rateLimiter) Observe(h http.Header) {
	limit, err := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	if err != nil || limit < 1 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if limit == r.perMin {
		return
	}
	r.perMin = limit
	r.lim.SetLimit(perMinute(limit))
	r.lim.SetBurst(limit)
}

type noopLimiter struct{}

func (noopLimiter) Wait(context.Context) error { return nil }
func (noopLimiter) Observe(http.Header)        {}

// isRateLimited separates a spent budget from an ordinary 403, which GitHub
// also uses for permission problems.
func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return resp.StatusCode == http.StatusForbidden &&
		resp.Header.Get("X-RateLimit-Remaining") == "0"
}

// resetTime reads whichever hint the response carries.
func resetTime(h http.Header) time.Time {
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(unix, 0)
		}
	}
	return time.Time{}
}

package github

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestNewRateLimiter(t *testing.T) {
	r := newRateLimiter(defaultRateLimit)

	if got, want := r.lim.Burst(), defaultRateLimit; got != want {
		t.Errorf("burst = %d, want %d", got, want)
	}
	if got, want := r.lim.Limit(), rate.Limit(10.0/60); got != want {
		t.Errorf("limit = %v, want %v", got, want)
	}
}

func TestRateLimiterBurst(t *testing.T) {
	r := newRateLimiter(defaultRateLimit)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := r.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestRateLimiterShortDeadline(t *testing.T) {
	// Burst spent, so the next slot is a minute away. A caller with 20ms left
	// should be told now rather than parked.
	r := newRateLimiter(1)
	if err := r.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := r.Wait(ctx)

	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("Wait() error = %v, want *RateLimitError", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Millisecond {
		t.Errorf("Wait blocked for %v, want an immediate answer", elapsed)
	}
	if rle.RetryAfter() == 0 {
		t.Error("RetryAfter() = 0, want time until the next slot")
	}
}

func TestRateLimiterCancel(t *testing.T) {
	r := newRateLimiter(1)
	if err := r.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if err := r.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() error = %v, want context.Canceled", err)
	}

	// The abandoned reservation should have gone back to the bucket.
	if tokens := r.lim.Tokens(); tokens < -0.5 {
		t.Errorf("tokens = %v, want the cancelled reservation returned", tokens)
	}
}

func TestRateLimiterObserve(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   int
	}{
		{name: "raised budget", header: "30", want: 30},
		{name: "lowered budget", header: "5", want: 5},
		{name: "missing header", header: "", want: defaultRateLimit},
		{name: "garbage header", header: "lots", want: defaultRateLimit},
		{name: "zero", header: "0", want: defaultRateLimit},
		{name: "negative", header: "-4", want: defaultRateLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRateLimiter(defaultRateLimit)

			h := http.Header{}
			if tt.header != "" {
				h.Set("X-RateLimit-Limit", tt.header)
			}
			r.Observe(h)

			if r.perMin != tt.want {
				t.Errorf("perMin = %d, want %d", r.perMin, tt.want)
			}
			if got, want := r.lim.Limit(), perMinute(tt.want); got != want {
				t.Errorf("limit = %v, want %v", got, want)
			}
		})
	}
}

func TestIsRateLimited(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		remaining string
		want      bool
	}{
		{name: "429", status: http.StatusTooManyRequests, want: true},
		{name: "403 with no budget left", status: http.StatusForbidden, remaining: "0", want: true},
		{name: "403 with budget left", status: http.StatusForbidden, remaining: "7"},
		{name: "403 with no header", status: http.StatusForbidden},
		{name: "500", status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tt.status, Header: http.Header{}}
			if tt.remaining != "" {
				resp.Header.Set("X-RateLimit-Remaining", tt.remaining)
			}

			if got := isRateLimited(resp); got != tt.want {
				t.Errorf("isRateLimited() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResetTime(t *testing.T) {
	t.Run("prefers Retry-After", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", "30")
		h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))

		got := time.Until(resetTime(h))
		if got < 25*time.Second || got > 35*time.Second {
			t.Errorf("reset in %v, want roughly 30s", got)
		}
	})

	t.Run("falls back to X-RateLimit-Reset", func(t *testing.T) {
		want := time.Now().Add(45 * time.Second).Truncate(time.Second)

		h := http.Header{}
		h.Set("X-RateLimit-Reset", strconv.FormatInt(want.Unix(), 10))

		if got := resetTime(h); !got.Equal(want) {
			t.Errorf("resetTime() = %v, want %v", got, want)
		}
	})

	t.Run("zero when absent", func(t *testing.T) {
		if got := resetTime(http.Header{}); !got.IsZero() {
			t.Errorf("resetTime() = %v, want zero", got)
		}
	})
}

func TestRetryAfter(t *testing.T) {
	t.Run("remaining time", func(t *testing.T) {
		e := &RateLimitError{Reset: time.Now().Add(20 * time.Second)}

		if got := e.RetryAfter(); got < 15*time.Second || got > 20*time.Second {
			t.Errorf("RetryAfter() = %v, want roughly 20s", got)
		}
	})

	t.Run("never negative", func(t *testing.T) {
		e := &RateLimitError{Reset: time.Now().Add(-time.Hour)}

		if got := e.RetryAfter(); got != 0 {
			t.Errorf("RetryAfter() = %v, want 0", got)
		}
	})
}

func TestSearchRateLimited(t *testing.T) {
	reset := time.Now().Add(42 * time.Second).Truncate(time.Second)

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message": "API rate limit exceeded"}`))
	})

	_, err := c.Search(context.Background(), Query{Term: "x"})

	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("Search() error = %v, want *RateLimitError", err)
	}
	if !rle.Reset.Equal(reset) {
		t.Errorf("Reset = %v, want %v", rle.Reset, reset)
	}
}

func TestSearchForbidden(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "9")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message": "Resource not accessible"}`))
	})

	_, err := c.Search(context.Background(), Query{Term: "x"})

	var rle *RateLimitError
	if errors.As(err, &rle) {
		t.Fatalf("Search() error = %v, want a plain API error", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Search() error = %v, want *APIError", err)
	}
}

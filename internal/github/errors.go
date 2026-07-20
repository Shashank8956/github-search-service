package github

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrMissingToken = errors.New("github: token is required")

	// Code search needs a real term, so "user:someone" on its own is rejected.
	ErrInvalidQuery = errors.New("github: invalid search query")

	ErrUnauthorized = errors.New("github: authentication failed")
)

// RateLimitError is a type rather than a sentinel because callers need the
// reset time to know when to come back.
type RateLimitError struct {
	Reset   time.Time
	Message string
}

func (e *RateLimitError) Error() string {
	if e.Reset.IsZero() {
		return "github: rate limit exceeded"
	}
	return fmt.Sprintf("github: rate limit exceeded, resets at %s",
		e.Reset.UTC().Format(time.RFC3339))
}

// RetryAfter is never negative, so a stale reset reads as "retry now".
func (e *RateLimitError) RetryAfter() time.Duration {
	if d := time.Until(e.Reset); d > 0 {
		return d
	}
	return 0
}

// APIError is any other unexpected response. Message is GitHub's own text.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("github: unexpected status %d", e.StatusCode)
	}
	return fmt.Sprintf("github: unexpected status %d: %s", e.StatusCode, e.Message)
}

package github

import (
	"errors"
	"fmt"
)

var (
	ErrMissingToken = errors.New("github: token is required")

	// Code search needs a real term, so "user:someone" on its own is rejected.
	ErrInvalidQuery = errors.New("github: invalid search query")

	ErrUnauthorized = errors.New("github: authentication failed")
)

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

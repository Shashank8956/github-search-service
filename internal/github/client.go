// Package github is a small client for the GitHub code search API.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.github.com"
	apiVersion     = "2022-11-28"

	// GitHub's maximum page size for code search.
	perPage = 100

	maxErrorBody = 4 << 10
)

// Result is a single code search hit.
type Result struct {
	FileURL string
	Repo    string
}

// Query is a code search. User is optional and scopes results to that account.
type Query struct {
	Term string
	User string
}

type Client struct {
	httpc   *http.Client
	baseURL string
	token   string
	limiter limiter
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpc = h }
}

// WithBaseURL aims the client at a different API root. Tests use it.
func WithBaseURL(raw string) Option {
	return func(c *Client) { c.baseURL = strings.TrimSuffix(raw, "/") }
}

// New returns a client. The token is required because code search rejects
// unauthenticated requests.
func New(token string, opts ...Option) (*Client, error) {
	if token == "" {
		return nil, ErrMissingToken
	}

	c := &Client{
		httpc:   &http.Client{Timeout: 30 * time.Second},
		baseURL: defaultBaseURL,
		token:   token,
		limiter: newRateLimiter(defaultRateLimit),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Search runs a code search and returns the matching files.
func (c *Client) Search(ctx context.Context, q Query) ([]Result, error) {
	term, err := buildQuery(q)
	if err != nil {
		return nil, err
	}

	page, err := c.searchPage(ctx, term, 1)
	if err != nil {
		return nil, err
	}
	return page.results(), nil
}

func buildQuery(q Query) (string, error) {
	term := strings.TrimSpace(q.Term)
	if term == "" {
		return "", fmt.Errorf("%w: search term is empty", ErrInvalidQuery)
	}

	if user := strings.TrimSpace(q.User); user != "" {
		return term + " user:" + user, nil
	}
	return term, nil
}

// searchResponse is the part of GitHub's payload we use.
type searchResponse struct {
	TotalCount int `json:"total_count"`
	Items      []struct {
		HTMLURL    string `json:"html_url"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	} `json:"items"`
}

func (r *searchResponse) results() []Result {
	out := make([]Result, 0, len(r.Items))
	for _, item := range r.Items {
		out = append(out, Result{
			FileURL: item.HTMLURL,
			Repo:    item.Repository.FullName,
		})
	}
	return out
}

func (c *Client) searchPage(ctx context.Context, term string, page int) (*searchResponse, error) {
	params := url.Values{}
	params.Set("q", term)
	params.Set("per_page", strconv.Itoa(perPage))
	params.Set("page", strconv.Itoa(page))

	endpoint := c.baseURL + "/search/code?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)

	// Wait for budget before spending it.
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("github: waiting for rate limit: %w", err)
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: search page %d: %w", page, err)
	}
	defer resp.Body.Close()

	c.limiter.Observe(resp.Header)

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}

	var body searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("github: decode page %d: %w", page, err)
	}
	return &body, nil
}

// statusError maps a non-200 onto our error values, never including the token.
func statusError(resp *http.Response) error {
	msg := readErrorMessage(resp.Body)

	if isRateLimited(resp) {
		return &RateLimitError{Reset: resetTime(resp.Header), Message: msg}
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusUnprocessableEntity:
		// GitHub uses 422 for queries it parsed but could not run.
		return fmt.Errorf("%w: %s", ErrInvalidQuery, msg)
	default:
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
}

func readErrorMessage(r io.Reader) string {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r, maxErrorBody)).Decode(&body); err != nil {
		return ""
	}
	return body.Message
}

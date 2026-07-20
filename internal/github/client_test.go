package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const twoHitsJSON = `{
  "total_count": 2,
  "items": [
    {
      "html_url": "https://github.com/octocat/hello/blob/main/main.go",
      "repository": {"full_name": "octocat/hello"}
    },
    {
      "html_url": "https://github.com/octocat/world/blob/main/util.go",
      "repository": {"full_name": "octocat/world"}
    }
  ]
}`

// pageJSON builds a response with n items, numbered from start.
func pageJSON(total, n, start int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(
			`{"html_url":"https://github.com/octocat/hello/blob/main/f%d.go","repository":{"full_name":"octocat/hello"}}`,
			start+i,
		)
	}
	return fmt.Sprintf(`{"total_count":%d,"items":[%s]}`, total, strings.Join(items, ","))
}

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Capping has its own tests, so the default does not cut these short.
	c, err := New("test-token",
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithMaxResults(maxPages*perPage),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Pacing has its own tests, so nothing here sleeps on a real clock.
	c.limiter = noopLimiter{}
	return c
}

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("New(\"\") = %v, want ErrMissingToken", err)
	}
}

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   Query
		want    string
		wantErr error
	}{
		{
			name:  "term only",
			query: Query{Term: "http.Client"},
			want:  "http.Client",
		},
		{
			name:  "term and user",
			query: Query{Term: "http.Client", User: "octocat"},
			want:  "http.Client user:octocat",
		},
		{
			name:  "whitespace trimmed",
			query: Query{Term: "  retry  ", User: "  octocat  "},
			want:  "retry user:octocat",
		},
		{
			name:    "empty term",
			query:   Query{Term: ""},
			wantErr: ErrInvalidQuery,
		},
		{
			name:    "user without term",
			query:   Query{User: "octocat"},
			wantErr: ErrInvalidQuery,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildQuery(tt.query)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("buildQuery(%+v) error = %v, want %v", tt.query, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildQuery(%+v): %v", tt.query, err)
			}
			if got != tt.want {
				t.Errorf("buildQuery(%+v) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(twoHitsJSON))
	})

	got, err := c.Search(context.Background(), Query{Term: "http.Client"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := []Result{
		{FileURL: "https://github.com/octocat/hello/blob/main/main.go", Repo: "octocat/hello"},
		{FileURL: "https://github.com/octocat/world/blob/main/util.go", Repo: "octocat/world"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Search() = %+v, want %+v", got, want)
	}
}

func TestSearchRequest(t *testing.T) {
	var got *http.Request
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Write([]byte(`{"total_count": 0, "items": []}`))
	})

	if _, err := c.Search(context.Background(), Query{Term: "retry", User: "octocat"}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got.URL.Path != "/search/code" {
		t.Errorf("path = %q, want /search/code", got.URL.Path)
	}
	if q := got.URL.Query().Get("q"); q != "retry user:octocat" {
		t.Errorf("q = %q, want %q", q, "retry user:octocat")
	}
	if pp := got.URL.Query().Get("per_page"); pp != "100" {
		t.Errorf("per_page = %q, want 100", pp)
	}
	if auth := got.Header.Get("Authorization"); auth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer test-token")
	}
	if accept := got.Header.Get("Accept"); accept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want application/vnd.github+json", accept)
	}
}

func TestSearchPaginates(t *testing.T) {
	var pages []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "1" {
			w.Write([]byte(pageJSON(150, perPage, 0)))
			return
		}
		w.Write([]byte(pageJSON(150, 50, 100)))
	})

	got, err := c.Search(context.Background(), Query{Term: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(got) != 150 {
		t.Errorf("got %d results, want 150", len(got))
	}
	if want := []string{"1", "2"}; !reflect.DeepEqual(pages, want) {
		t.Errorf("fetched pages %v, want %v", pages, want)
	}
}

func TestSearchStopsAtLastPage(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Always a full page, so only the 1000 result ceiling ends the loop.
		w.Write([]byte(pageJSON(5000, perPage, 0)))
	})

	if _, err := c.Search(context.Background(), Query{Term: "x"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if calls != maxPages {
		t.Errorf("made %d requests, want %d", calls, maxPages)
	}
}

func TestSearchMaxResults(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(pageJSON(5000, perPage, 0)))
	})
	c.maxResults = 150

	got, err := c.Search(context.Background(), Query{Term: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(got) != 150 {
		t.Errorf("got %d results, want 150", len(got))
	}
	// Stops as soon as the cap is met instead of walking all ten pages.
	if calls != 2 {
		t.Errorf("made %d requests, want 2", calls)
	}
}

func TestNewRejectsBadMaxResults(t *testing.T) {
	if _, err := New("test-token", WithMaxResults(0)); err == nil {
		t.Fatal("New(WithMaxResults(0)) = nil error, want one")
	}
}

func TestSearchCoalesces(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		w.Write([]byte(twoHitsJSON))
	})

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Search(context.Background(), Query{Term: "same"}); err != nil {
				t.Errorf("Search: %v", err)
			}
		}()
	}

	// Let the callers pile up behind the one that is in flight.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Errorf("made %d API calls, want 1", n)
	}
}

func TestSearchCancelOne(t *testing.T) {
	release := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write([]byte(twoHitsJSON))
	})

	ctx, cancel := context.WithCancel(context.Background())
	go c.Search(ctx, Query{Term: "same"})
	time.Sleep(50 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := c.Search(context.Background(), Query{Term: "same"})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)

	// The first caller leaves; the second is still waiting on the same call.
	cancel()
	close(release)

	if err := <-done; err != nil {
		t.Errorf("second caller got %v, want it to still succeed", err)
	}
}

func TestSearchNoResults(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total_count": 0, "items": []}`))
	})

	got, err := c.Search(context.Background(), Query{Term: "nothing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Search() = %+v, want empty", got)
	}
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{
			name:    "unauthorized",
			status:  http.StatusUnauthorized,
			body:    `{"message": "Bad credentials"}`,
			wantErr: ErrUnauthorized,
		},
		{
			name:    "unprocessable query",
			status:  http.StatusUnprocessableEntity,
			body:    `{"message": "Validation Failed"}`,
			wantErr: ErrInvalidQuery,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			})

			_, err := c.Search(context.Background(), Query{Term: "x"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Search() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSearchServerError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message": "Server Error"}`))
	})

	_, err := c.Search(context.Background(), Query{Term: "x"})

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Search() error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if apiErr.Message != "Server Error" {
		t.Errorf("Message = %q, want %q", apiErr.Message, "Server Error")
	}
}

func TestSearchMalformedJSON(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items": [`))
	})

	if _, err := c.Search(context.Background(), Query{Term: "x"}); err == nil {
		t.Fatal("Search() error = nil, want a decode error")
	}
}

func TestSearchCancelled(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(twoHitsJSON))
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Search(ctx, Query{Term: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search() error = %v, want context.Canceled", err)
	}
}

func TestNoTokenInErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message": "boom"}`))
	})

	_, err := c.Search(context.Background(), Query{Term: "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "test-token") {
		t.Errorf("error leaked the token: %v", err)
	}
}

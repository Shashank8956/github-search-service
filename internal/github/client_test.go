package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := New("test-token", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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

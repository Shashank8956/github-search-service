package server

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shashank8956/github-search-service/internal/github"
	"github.com/shashank8956/github-search-service/proto/searchpb"
)

type stubSearcher struct {
	got     github.Query
	results []github.Result
	err     error
}

func (s *stubSearcher) Search(_ context.Context, q github.Query) ([]github.Result, error) {
	s.got = q
	return s.results, s.err
}

func TestSearch(t *testing.T) {
	stub := &stubSearcher{results: []github.Result{
		{FileURL: "https://github.com/octocat/hello/blob/main/main.go", Repo: "octocat/hello"},
	}}
	svc, err := New(stub)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := svc.Search(context.Background(), &searchpb.SearchRequest{
		SearchTerm: "retry",
		User:       "octocat",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if want := (github.Query{Term: "retry", User: "octocat"}); stub.got != want {
		t.Errorf("forwarded %+v, want %+v", stub.got, want)
	}
	if n := len(resp.GetResults()); n != 1 {
		t.Fatalf("got %d results, want 1", n)
	}
	if got := resp.GetResults()[0].GetRepo(); got != "octocat/hello" {
		t.Errorf("repo = %q, want octocat/hello", got)
	}
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"invalid query", github.ErrInvalidQuery, codes.InvalidArgument},
		{"rate limited", &github.RateLimitError{}, codes.ResourceExhausted},
		{"bad credentials", github.ErrUnauthorized, codes.Unavailable},
		{"timed out", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"anything else", errors.New("boom"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := New(&stubSearcher{err: tt.err})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			_, gotErr := svc.Search(context.Background(), &searchpb.SearchRequest{SearchTerm: "x"})
			if got := status.Code(gotErr); got != tt.want {
				t.Errorf("code = %v, want %v", got, tt.want)
			}
		})
	}
}

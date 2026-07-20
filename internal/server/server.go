// Package server exposes the GitHub search over gRPC.
package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shashank8956/github-search-service/internal/github"
	"github.com/shashank8956/github-search-service/proto/searchpb"
)

// Searcher is the slice of the GitHub client this service needs.
type Searcher interface {
	Search(ctx context.Context, q github.Query) ([]github.Result, error)
}

type Service struct {
	searchpb.UnimplementedGithubSearchServiceServer
	searcher Searcher
}

func New(s Searcher) (*Service, error) {
	if s == nil {
		return nil, errors.New("server: searcher is required")
	}
	return &Service{searcher: s}, nil
}

func (s *Service) Search(ctx context.Context, req *searchpb.SearchRequest) (*searchpb.SearchResponse, error) {
	found, err := s.searcher.Search(ctx, github.Query{
		Term: req.GetSearchTerm(),
		User: req.GetUser(),
	})
	if err != nil {
		return nil, toStatus(err)
	}

	results := make([]*searchpb.Result, 0, len(found))
	for _, r := range found {
		results = append(results, &searchpb.Result{FileUrl: r.FileURL, Repo: r.Repo})
	}
	return &searchpb.SearchResponse{Results: results}, nil
}

// toStatus maps our errors onto gRPC codes. Anything unrecognised becomes a
// plain Internal so upstream detail does not leak to callers.
func toStatus(err error) error {
	var rateLimit *github.RateLimitError

	switch {
	case errors.Is(err, github.ErrInvalidQuery):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.As(err, &rateLimit):
		return status.Error(codes.ResourceExhausted, rateLimit.Error())
	case errors.Is(err, github.ErrUnauthorized):
		return status.Error(codes.Unavailable, "github rejected our credentials")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "search timed out")
	default:
		return status.Error(codes.Internal, "search failed")
	}
}

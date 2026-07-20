package server

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var testInfo = &grpc.UnaryServerInfo{FullMethod: "/githubsearch.v1.GithubSearchService/Search"}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestRecover(t *testing.T) {
	handler := func(context.Context, any) (any, error) { panic("boom") }

	_, err := Recover(discardLogger())(context.Background(), nil, testInfo, handler)
	if got := status.Code(err); got != codes.Internal {
		t.Errorf("code = %v, want %v", got, codes.Internal)
	}
}

func TestLogRequests(t *testing.T) {
	want := errors.New("failed")
	handler := func(context.Context, any) (any, error) { return "ok", want }

	resp, err := LogRequests(discardLogger())(context.Background(), nil, testInfo, handler)
	if resp != "ok" || !errors.Is(err, want) {
		t.Errorf("got (%v, %v), want (ok, %v)", resp, err, want)
	}
}

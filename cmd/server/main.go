package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/shashank8956/github-search-service/internal/github"
	"github.com/shashank8956/github-search-service/internal/server"
	"github.com/shashank8956/github-search-service/proto/searchpb"
)

func main() {
	if err := run(); err != nil {
		slog.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", ":50051", "address to listen on")
	maxResults := flag.Int("max-results", 100, "most results to return per search")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// From the environment rather than a flag, so it stays out of ps output.
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return errors.New("GITHUB_TOKEN is not set")
	}

	client, err := github.New(token, github.WithMaxResults(*maxResults))
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}

	svc, err := server.New(client)
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *addr, err)
	}

	// Recover sits outermost so it also covers the logging interceptor.
	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		server.Recover(logger),
		server.LogRequests(logger),
	))
	searchpb.RegisterGithubSearchServiceServer(grpcServer, svc)
	reflection.Register(grpcServer) // lets grpcurl explore the service

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		logger.Info("shutting down")
		grpcServer.GracefulStop()
	}()

	logger.Info("listening", "addr", *addr)
	return grpcServer.Serve(lis)
}

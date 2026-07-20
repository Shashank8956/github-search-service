package main

import (
	"flag"
	"log"
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
	addr := flag.String("addr", ":50051", "address to listen on")
	flag.Parse()

	// From the environment rather than a flag, so it stays out of ps output.
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN is not set")
	}

	client, err := github.New(token)
	if err != nil {
		log.Fatalf("github client: %v", err)
	}

	svc, err := server.New(client)
	if err != nil {
		log.Fatalf("service: %v", err)
	}

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen on %s: %v", *addr, err)
	}

	grpcServer := grpc.NewServer()
	searchpb.RegisterGithubSearchServiceServer(grpcServer, svc)
	reflection.Register(grpcServer) // lets grpcurl explore the service

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		log.Println("shutting down")
		grpcServer.GracefulStop()
	}()

	log.Printf("listening on %s", *addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

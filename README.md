# github-search-service

A gRPC service that searches code on GitHub. Give it a phrase and an optional
user, and it returns the file URL and repo for each match.

## Setup

Needs Go 1.22+ and a GitHub token (classic token with `public_repo` scope).

```
git clone https://github.com/Shashank8956/github-search-service.git
cd github-search-service
go mod download
```

## Run

```
set GITHUB_TOKEN=your_token
go run ./cmd/server
```

Then call it with grpcurl:

```
grpcurl -plaintext -d "{\"search_term\":\"rate limiter\",\"user\":\"golang\"}" localhost:50051 githubsearch.v1.GithubSearchService/Search
```

`user` is optional. Run the tests with `go test ./...`.

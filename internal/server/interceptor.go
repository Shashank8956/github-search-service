package server

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Recover turns a panic in a handler into an Internal error. grpc-go does not
// do this itself, so one nil dereference would otherwise kill the process.
func Recover(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				log.Error("panic", "method", info.FullMethod, "value", p, "stack", string(debug.Stack()))
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}

// LogRequests writes one line per call with its status code and duration.
func LogRequests(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		log.Info("request",
			"method", info.FullMethod,
			"code", status.Code(err).String(),
			"took", time.Since(start),
		)
		return resp, err
	}
}

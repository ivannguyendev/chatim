package grpcserver

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/resilience"
)

func LoadShedUnary(l *resilience.Limiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if strings.HasPrefix(info.FullMethod, "/grpc.health.v1.Health/") {
			return handler(ctx, req)
		}
		if err := l.Acquire(ctx); err != nil {
			if errors.Is(err, resilience.ErrOverloaded) {
				return nil, status.Error(codes.Unavailable, "server overloaded, retry with backoff")
			}
			return nil, status.FromContextError(err).Err()
		}
		defer l.Release()
		return handler(ctx, req)
	}
}

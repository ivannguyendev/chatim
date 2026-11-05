package grpcserver

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type drainingHealth struct {
	*health.Server
	draining <-chan struct{}
}

func (h *drainingHealth) Watch(req *healthpb.HealthCheckRequest, stream healthpb.Health_WatchServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	go func() {
		select {
		case <-h.draining:
			cancel()
		case <-ctx.Done():
		}
	}()
	return h.Server.Watch(req, &ctxStream[healthpb.HealthCheckResponse]{
		ServerStreamingServer: stream,
		ctx:                   ctx,
	})
}

type ctxStream[T any] struct {
	grpc.ServerStreamingServer[T]
	ctx context.Context
}

func (s *ctxStream[T]) Context() context.Context { return s.ctx }

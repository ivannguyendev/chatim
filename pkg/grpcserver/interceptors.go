package grpcserver

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func RecoveryUnary(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = panicToStatus(ctx, logger, info.FullMethod, r)
			}
		}()
		return handler(ctx, req)
	}
}

func RecoveryStream(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = panicToStatus(ss.Context(), logger, info.FullMethod, r)
			}
		}()
		return handler(srv, ss)
	}
}

func panicToStatus(ctx context.Context, logger *slog.Logger, method string, r any) error {
	logger.ErrorContext(ctx, "panic in grpc handler",
		"grpc.method", method, "panic", r, "stack", string(debug.Stack()))
	return status.Error(codes.Internal, "internal error")
}

func LoggingUnary(logger *slog.Logger, slow time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logRPC(ctx, logger, info.FullMethod, time.Since(start), slow, err)
		return resp, err
	}
}

func LoggingStream(logger *slog.Logger, slow time.Duration) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		logRPC(ss.Context(), logger, info.FullMethod, time.Since(start), slow, err)
		return err
	}
}

func logRPC(ctx context.Context, logger *slog.Logger, method string, took, slow time.Duration, err error) {
	code := status.Code(err)
	var level slog.Level
	switch {
	case isServerFault(code):
		level = slog.LevelError
	case code != codes.OK:
		level = slog.LevelInfo
	case took >= slow:
		level = slog.LevelWarn
	default:
		return
	}
	logger.Log(ctx, level, "rpc finished",
		"grpc.method", method,
		"grpc.code", code.String(),
		"duration_ms", float64(took.Microseconds())/1000,
	)
}

func isServerFault(c codes.Code) bool {
	switch c {
	case codes.Unknown, codes.Internal, codes.DataLoss, codes.Unavailable,
		codes.DeadlineExceeded, codes.Unimplemented:
		return true
	default:
		return false
	}
}

func ErrorBoundaryUnary(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		return resp, boundary(ctx, logger, info.FullMethod, err)
	}
}

func ErrorBoundaryStream(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return boundary(ss.Context(), logger, info.FullMethod, handler(srv, ss))
	}
}

func boundary(ctx context.Context, logger *slog.Logger, method string, err error) error {
	st := ToStatus(err)
	code := status.Code(st)
	_, direct := err.(interface{ GRPCStatus() *status.Status })
	if code == codes.Internal || code == codes.Unknown || (!direct && isServerFault(code)) {
		logger.ErrorContext(ctx, "grpc handler error",
			"grpc.method", method, "grpc.code", code.String(), "err", err)
	}
	return st
}

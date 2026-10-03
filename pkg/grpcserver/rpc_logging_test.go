package grpcserver_test

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

const slowRPC = 500 * time.Millisecond

type rpcCase struct {
	name  string
	took  time.Duration
	err   error
	level []slog.Level
}

var rpcCases = []rpcCase{
	{"fast success is silent", slowRPC - time.Millisecond, nil, nil},
	{"slow success warns", slowRPC, nil, []slog.Level{slog.LevelWarn}},
	{"client error is info", time.Millisecond, status.Error(codes.NotFound, "no room"), []slog.Level{slog.LevelInfo}},
	{"slow client error is logged once", 2 * slowRPC, status.Error(codes.InvalidArgument, "bad"), []slog.Level{slog.LevelInfo}},
	{"server fault is error", time.Millisecond, status.Error(codes.Unavailable, "down"), []slog.Level{slog.LevelError}},
}

func TestLoggingUnaryKeepsOnlyFailedAndSlowCalls(t *testing.T) {
	for _, tc := range rpcCases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &rpcLogSink{}
				intercept := grpcserver.LoggingUnary(slog.New(sink), slowRPC)
				_, err := intercept(t.Context(), nil, &grpc.UnaryServerInfo{FullMethod: fakeMethod}, func(context.Context, any) (any, error) {
					time.Sleep(tc.took)
					return nil, tc.err
				})
				if status.Code(err) != status.Code(tc.err) {
					t.Fatalf("interceptor returned %v, want %v", err, tc.err)
				}
				sink.expect(t, tc.level)
			})
		})
	}
}

func TestLoggingStreamKeepsOnlyFailedAndSlowCalls(t *testing.T) {
	for _, tc := range rpcCases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &rpcLogSink{}
				intercept := grpcserver.LoggingStream(slog.New(sink), slowRPC)
				err := intercept(nil, contextStream{ctx: t.Context()}, &grpc.StreamServerInfo{FullMethod: fakeMethod}, func(any, grpc.ServerStream) error {
					time.Sleep(tc.took)
					return tc.err
				})
				if status.Code(err) != status.Code(tc.err) {
					t.Fatalf("interceptor returned %v, want %v", err, tc.err)
				}
				sink.expect(t, tc.level)
			})
		})
	}
}

func TestServerLogsCallsSlowerThanTheDefaultThreshold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &rpcLogSink{}
		var took time.Duration
		h := startLogged(t, grpcserver.Config{}, slog.New(sink), func(context.Context) error {
			time.Sleep(took)
			return nil
		})
		took = grpcserver.DefaultSlowRPC - time.Millisecond
		if err := callFake(t.Context(), h.conn); err != nil {
			t.Fatalf("fast call: %v", err)
		}
		took = grpcserver.DefaultSlowRPC
		if err := callFake(t.Context(), h.conn); err != nil {
			t.Fatalf("slow call: %v", err)
		}
		sink.expect(t, []slog.Level{slog.LevelWarn})
	})
}

type contextStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s contextStream) Context() context.Context { return s.ctx }

type rpcLogSink struct {
	mu     sync.Mutex
	levels []slog.Level
}

func (s *rpcLogSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *rpcLogSink) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "rpc finished" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.levels = append(s.levels, r.Level)
	return nil
}

func (s *rpcLogSink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *rpcLogSink) WithGroup(string) slog.Handler { return s }

func (s *rpcLogSink) expect(t *testing.T, want []slog.Level) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Equal(s.levels, want) {
		t.Fatalf("rpc log levels = %v, want %v", s.levels, want)
	}
}

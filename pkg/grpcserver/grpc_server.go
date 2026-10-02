package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/ivannguyendev/chatim/pkg/resilience"
)

const DefaultSlowRPC = 500 * time.Millisecond

type Config struct {
	Addr             string
	ShutdownTimeout  time.Duration
	DrainDelay       time.Duration
	RequestDeadline  time.Duration
	SlowRPC          time.Duration
	EnableReflection bool
	Limiter          *resilience.Limiter
	ServerOptions    []grpc.ServerOption
}

type Server struct {
	cfg      Config
	grpc     *grpc.Server
	health   *health.Server
	draining chan struct{}
	logger   *slog.Logger
}

func New(cfg Config, logger *slog.Logger) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":50051"
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 15 * time.Second
	}
	if cfg.SlowRPC <= 0 {
		cfg.SlowRPC = DefaultSlowRPC
	}

	unary := []grpc.UnaryServerInterceptor{RecoveryUnary(logger)}
	if cfg.RequestDeadline > 0 {
		unary = append(unary, DeadlineUnary(cfg.RequestDeadline))
	}
	if cfg.Limiter != nil {
		unary = append(unary, LoadShedUnary(cfg.Limiter))
	}
	unary = append(unary, LoggingUnary(logger, cfg.SlowRPC), ErrorBoundaryUnary(logger))

	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(RecoveryStream(logger), LoggingStream(logger, cfg.SlowRPC), ErrorBoundaryStream(logger)),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     5 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 30 * time.Second,
			Time:                  time.Minute,
			Timeout:               20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             15 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.MaxRecvMsgSize(4 << 20),
	}
	opts = append(opts, cfg.ServerOptions...)

	s := &Server{
		cfg:      cfg,
		grpc:     grpc.NewServer(opts...),
		health:   health.NewServer(),
		draining: make(chan struct{}),
		logger:   logger,
	}
	healthpb.RegisterHealthServer(s.grpc, &drainingHealth{Server: s.health, draining: s.draining})
	if cfg.EnableReflection {
		reflection.Register(s.grpc)
	}
	return s
}

func (s *Server) RegisterService(desc *grpc.ServiceDesc, impl any) {
	s.grpc.RegisterService(desc, impl)
}

func (s *Server) Draining() <-chan struct{} { return s.draining }

func (s *Server) Health() *health.Server { return s.health }

func (s *Server) Serve(ctx context.Context) error {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.Addr, err)
	}
	return s.ServeListener(ctx, lis)
}

func (s *Server) ServeListener(ctx context.Context, lis net.Listener) error {
	for name := range s.grpc.GetServiceInfo() {
		s.health.SetServingStatus(name, healthpb.HealthCheckResponse_SERVING)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.grpc.Serve(lis) }()
	s.logger.InfoContext(ctx, "grpc server listening", "addr", lis.Addr().String())

	select {
	case err := <-errCh:
		s.grpc.Stop()
		return fmt.Errorf("grpc serve: %w", err)
	case <-ctx.Done():
	}
	s.shutdown()
	if err := <-errCh; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

func (s *Server) shutdown() {
	s.health.Shutdown()
	if s.cfg.DrainDelay > 0 {
		time.Sleep(s.cfg.DrainDelay)
	}
	close(s.draining)

	stopped := make(chan struct{})
	go func() {
		s.grpc.GracefulStop()
		close(stopped)
	}()

	timer := time.NewTimer(s.cfg.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-stopped:
		s.logger.Info("grpc server stopped gracefully")
	case <-timer.C:
		s.logger.Warn("graceful stop timed out, forcing close", "timeout", s.cfg.ShutdownTimeout)
		s.grpc.Stop()
		<-stopped
	}
}

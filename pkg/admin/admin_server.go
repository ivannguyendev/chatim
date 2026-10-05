package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"sync/atomic"
)

type Server struct {
	cfg    Config
	srv    *http.Server
	logger *slog.Logger
	ready  atomic.Bool
}

func New(cfg Config, logger *slog.Logger) *Server {
	cfg = cfg.withDefaults()
	s := &Server{cfg: cfg, logger: logger}
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	return s
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !s.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	if s.cfg.Metrics != nil {
		mux.Handle("GET /metrics", s.cfg.Metrics)
	}
	return mux
}

func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) Addr() string { return s.cfg.Addr }

func (s *Server) ServeListener(ctx context.Context, lis net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.Serve(lis) }()

	select {
	case err := <-errCh:
		_ = s.srv.Close()
		return fmt.Errorf("admin serve %s: %w", lis.Addr(), err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		s.logger.WarnContext(ctx, "admin graceful shutdown did not finish, closing connections",
			"addr", lis.Addr().String(), "timeout", s.cfg.ShutdownTimeout, "err", err)
		if cerr := s.srv.Close(); cerr != nil {
			return fmt.Errorf("admin close: %w", cerr)
		}
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("admin serve: %w", err)
	}
	return nil
}

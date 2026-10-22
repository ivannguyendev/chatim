package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lxzan/gws"
	"golang.org/x/time/rate"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const subprotocolHeader = "Sec-WebSocket-Protocol"

type Server struct {
	opts      Options
	verifier  Verifier
	executor  Executor
	interest  Interest
	logger    *slog.Logger
	upgrader  *gws.Upgrader
	stats     *stats
	accepting *gate
	commands  *gate
	authLog   *rate.Limiter

	mu        sync.Mutex
	live      map[*connection]struct{}
	closeCode uint16
}

func New(opts Options, verifier Verifier, executor Executor, rooms Interest, logger *slog.Logger) (*Server, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if verifier == nil || executor == nil || rooms == nil || logger == nil {
		return nil, errors.New("session: verifier, executor, interest and logger are required")
	}
	s := &Server{
		opts:      opts,
		verifier:  verifier,
		executor:  executor,
		interest:  rooms,
		logger:    logger,
		stats:     newStats(),
		accepting: newGate(),
		commands:  newGate(),
		authLog:   rate.NewLimiter(rate.Every(time.Second), 1),
		live:      map[*connection]struct{}{},
	}
	s.upgrader = gws.NewUpgrader(events{}, &gws.ServerOption{
		ReadMaxPayloadSize: opts.FrameMax,
		SubProtocols:       []string{Subprotocol},
		PermessageDeflate:  gws.PermessageDeflate{Enabled: false},
		Recovery:           s.recoverHandler,
		Logger:             gwsLogger{logger: logger},
	})
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if status := s.admit(r); status != http.StatusOK {
		http.Error(w, http.StatusText(status), status)
		return
	}
	socket, err := s.upgrader.Upgrade(w, r)
	s.serveUpgraded(socket, err)
}

func (s *Server) Sources() []metrics.Source { return s.stats.sources() }

func (s *Server) Drain(ctx context.Context) error {
	s.accepting.close()
	return s.commands.wait(ctx)
}

func (s *Server) CloseAll(code uint16) {
	s.mu.Lock()
	s.closeCode = code
	conns := make([]*connection, 0, len(s.live))
	for c := range s.live {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.closeWith(code)
	}
}

func (s *Server) Wait(ctx context.Context) error { return s.accepting.wait(ctx) }

func (s *Server) Shutdown(ctx context.Context) error {
	drained := s.Drain(ctx)
	s.CloseAll(CodeGoingAway)
	return errors.Join(drained, s.Wait(ctx))
}

func (s *Server) admit(r *http.Request) int {
	if !offersSubprotocol(r.Header) {
		return http.StatusBadRequest
	}
	if !s.accepting.enter() {
		return http.StatusServiceUnavailable
	}
	if s.stats.unauth.Add(1) > int64(s.opts.UnauthMax) {
		s.stats.unauth.Add(-1)
		s.accepting.leave()
		return http.StatusServiceUnavailable
	}
	return http.StatusOK
}

func (s *Server) serveUpgraded(socket *gws.Conn, err error) {
	defer s.accepting.leave()
	if err != nil {
		s.stats.unauth.Add(-1)
		s.logger.Debug("websocket upgrade failed", "error", err)
		return
	}
	c := newConnection(s, socket)
	s.track(c)
	defer s.untrack(c)
	c.run()
}

func (s *Server) track(c *connection) {
	s.stats.open.Add(1)
	s.mu.Lock()
	s.live[c] = struct{}{}
	code := s.closeCode
	s.mu.Unlock()
	if code != 0 {
		c.closeWith(code)
	}
}

func (s *Server) untrack(c *connection) {
	s.mu.Lock()
	delete(s.live, c)
	s.mu.Unlock()
	s.stats.open.Add(-1)
}

func (s *Server) authFailed(label string) {
	s.stats.authFailed(label)
	if s.authLog.Allow() {
		s.logger.Warn("websocket auth failed", "reason", label)
	}
}

func (s *Server) recoverHandler(gws.Logger) {
	if r := recover(); r != nil {
		s.logger.Error("websocket handler panic", "panic", fmt.Sprint(r))
	}
}

func offersSubprotocol(h http.Header) bool {
	for offered := range strings.SplitSeq(h.Get(subprotocolHeader), ",") {
		if strings.TrimSpace(offered) == Subprotocol {
			return true
		}
	}
	return false
}

type gwsLogger struct {
	logger *slog.Logger
}

func (l gwsLogger) Error(v ...any) { l.logger.Error("gws error", "detail", fmt.Sprint(v...)) }

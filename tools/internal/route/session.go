package route

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

type SessionConfig struct {
	RedisAddr     string
	RedisDB       int
	RedisPassword string
	ClientName    string
	Policy        Policy
	ReadyTimeout  time.Duration
	Log           *slog.Logger
	Dial          Dialer
}

type Session struct {
	Resolver *slotmap.Resolver
	Client   *Client
	router   *coreroute.Router
}

func Open(ctx context.Context, cfg SessionConfig) (*Session, error) {
	router, err := coreroute.Open(ctx, coreroute.Config{
		RedisAddr: cfg.RedisAddr, RedisDB: cfg.RedisDB, RedisPassword: cfg.RedisPassword, ClientName: cfg.ClientName,
		ReadyTimeout: cfg.ReadyTimeout, Log: cfg.Log, Dial: cfg.Dial,
	})
	if err != nil {
		return nil, err
	}
	client, err := newClient(router, cfg.Policy)
	if err != nil {
		return nil, errors.Join(err, router.Close())
	}
	return &Session{Resolver: router.Resolver, Client: client, router: router}, nil
}

func (s *Session) Close() error { return s.router.Close() }

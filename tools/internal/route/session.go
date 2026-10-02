package route

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ivannguyendev/chatim/pkg/grpcclient"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const DefaultReadyTimeout = 10 * time.Second

type SessionConfig struct {
	RedisAddr    string
	RedisDB      int
	ClientName   string
	Policy       Policy
	ReadyTimeout time.Duration
	Log          *slog.Logger
	Dial         Dialer
}

type Session struct {
	Resolver *slotmap.Resolver
	Client   *Client
	rdb      *redis.Client
	stop     context.CancelFunc
	done     chan error
}

func Open(ctx context.Context, cfg SessionConfig) (*Session, error) {
	if cfg.ReadyTimeout < 0 {
		return nil, errors.New("route: ready timeout must not be negative")
	}
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, DB: cfg.RedisDB, ClientName: cfg.ClientName, ContextTimeoutEnabled: true})
	res, err := slotmap.NewResolver(rdb, slotmap.ResolverConfig{}, cfg.Log)
	if err != nil {
		return nil, errors.Join(err, rdb.Close())
	}
	dial := cfg.Dial
	if dial == nil {
		dial = DialInsecure
	}
	client, err := New(res, dial, cfg.Policy)
	if err != nil {
		return nil, errors.Join(err, rdb.Close())
	}
	runCtx, stop := context.WithCancel(ctx)
	s := &Session{Resolver: res, Client: client, rdb: rdb, stop: stop, done: make(chan error, 1)}
	go func() { s.done <- res.Run(runCtx) }()
	wait := cmp.Or(cfg.ReadyTimeout, DefaultReadyTimeout)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-res.Ready():
		return s, nil
	case <-timer.C:
		err = fmt.Errorf("slot table not loaded from redis %s within %v", cfg.RedisAddr, wait)
	case <-ctx.Done():
		err = ctx.Err()
	}
	return nil, errors.Join(err, s.Close())
}

func (s *Session) Close() error {
	s.stop()
	return errors.Join(<-s.done, s.Client.Close(), s.rdb.Close())
}

func DialInsecure(addr string) (chatimv1.CoreServiceClient, io.Closer, error) {
	cc, err := grpcclient.New(addr, grpcclient.Options{Creds: insecure.NewCredentials()})
	if err != nil {
		return nil, nil, err
	}
	return chatimv1.NewCoreServiceClient(cc), cc, nil
}

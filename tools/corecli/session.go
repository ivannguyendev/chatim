package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ivannguyendev/chatim/pkg/grpcclient"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/route"
)

const readyTimeout = 10 * time.Second

type options struct {
	redis    string
	redisDB  int
	tenant   string
	user     string
	deadline time.Duration
	attempt  time.Duration
}

func addOptions(fs *flag.FlagSet) *options {
	o := &options{}
	fs.StringVar(&o.redis, "redis", cmp.Or(os.Getenv("REDIS_ADDR"), "chatim-redis:6379"), "Redis holding slot leases (env REDIS_ADDR)")
	fs.IntVar(&o.redisDB, "redis-db", 0, "Redis database index of the cores")
	fs.StringVar(&o.tenant, "tenant", "e2e", "caller tenant sent as x-chatim-tenant")
	fs.StringVar(&o.user, "user", "e2e-user", "caller user sent as x-chatim-user")
	fs.DurationVar(&o.deadline, "deadline", time.Minute, "give up one call after this long, retries included")
	fs.DurationVar(&o.attempt, "attempt", 5*time.Second, "timeout of one attempt")
	return o
}

type session struct {
	rdb    *redis.Client
	res    *slotmap.Resolver
	client *route.Client
	stop   context.CancelFunc
	done   chan error
}

func withSession(ctx context.Context, o *options, run func(ctx context.Context, s *session) error) error {
	s, err := openSession(ctx, o)
	if err != nil {
		return err
	}
	return errors.Join(run(route.WithCaller(ctx, o.tenant, o.user), s), s.close())
}

func openSession(ctx context.Context, o *options) (*session, error) {
	rdb := redis.NewClient(&redis.Options{Addr: o.redis, DB: o.redisDB, ClientName: "chatim-corecli", ContextTimeoutEnabled: true})
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	res, err := slotmap.NewResolver(rdb, slotmap.ResolverConfig{}, log)
	if err != nil {
		return nil, errors.Join(err, rdb.Close())
	}
	client, err := route.New(res, dialCore, route.Policy{Deadline: o.deadline, Attempt: o.attempt})
	if err != nil {
		return nil, errors.Join(err, rdb.Close())
	}
	runCtx, stop := context.WithCancel(ctx)
	s := &session{rdb: rdb, res: res, client: client, stop: stop, done: make(chan error, 1)}
	go func() { s.done <- res.Run(runCtx) }()
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-res.Ready():
		return s, nil
	case <-timer.C:
		err = fmt.Errorf("slot table not loaded from redis %s within %v", o.redis, readyTimeout)
	case <-ctx.Done():
		err = ctx.Err()
	}
	return nil, errors.Join(err, s.close())
}

func (s *session) close() error {
	s.stop()
	return errors.Join(<-s.done, s.client.Close(), s.rdb.Close())
}

func dialCore(addr string) (chatimv1.CoreServiceClient, io.Closer, error) {
	cc, err := grpcclient.New(addr, grpcclient.Options{Creds: insecure.NewCredentials()})
	if err != nil {
		return nil, nil, err
	}
	return chatimv1.NewCoreServiceClient(cc), cc, nil
}

func report(call string, st route.Stats) {
	fmt.Fprintf(os.Stderr, "%s via %s in %d attempt(s)\n", call, st.Addr, st.Attempts)
}

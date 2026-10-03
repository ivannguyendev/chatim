package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type options struct {
	redis    string
	redisDB  int
	redisPW  string
	tenant   string
	user     string
	deadline time.Duration
	attempt  time.Duration
}

func addOptions(fs *flag.FlagSet) *options {
	o := &options{}
	fs.StringVar(&o.redis, "redis", cmp.Or(os.Getenv("REDIS_ADDR"), "chatim-redis:6379"), "Redis holding slot leases (env REDIS_ADDR)")
	fs.IntVar(&o.redisDB, "redis-db", 0, "Redis database index of the cores")
	fs.StringVar(&o.redisPW, "redis-password", "", "password of that Redis; env REDIS_PASSWORD when empty")
	fs.StringVar(&o.tenant, "tenant", "e2e", "caller tenant sent as x-chatim-tenant")
	fs.StringVar(&o.user, "user", "e2e-user", "caller user sent as x-chatim-user")
	fs.DurationVar(&o.deadline, "deadline", time.Minute, "give up one call after this long, retries included")
	fs.DurationVar(&o.attempt, "attempt", 5*time.Second, "timeout of one attempt")
	return o
}

type session struct {
	res    *slotmap.Resolver
	client *route.Client
}

func withSession(ctx context.Context, o *options, run func(ctx context.Context, s *session) error) error {
	rs, err := route.Open(ctx, route.SessionConfig{
		RedisAddr:     o.redis,
		RedisDB:       o.redisDB,
		RedisPassword: cmp.Or(o.redisPW, os.Getenv("REDIS_PASSWORD")),
		ClientName:    "chatim-corecli",
		Policy:        route.Policy{Deadline: o.deadline, Attempt: o.attempt},
		Log:           slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		return err
	}
	s := &session{res: rs.Resolver, client: rs.Client}
	return errors.Join(run(route.WithCaller(ctx, o.tenant, o.user), s), rs.Close())
}

func report(call string, st route.Stats) {
	fmt.Fprintf(os.Stderr, "%s via %s in %d attempt(s)\n", call, st.Addr, st.Attempts)
}

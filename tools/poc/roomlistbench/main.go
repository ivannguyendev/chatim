package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type config struct {
	mode, layoutPath                               string
	redis, redisPW, tenant, core, metrics, mongoDB string
	sizes                                          []int
	perSize, private, privateMessages, page        int
	backgroundMessages, workers, joinMax, repeats  int
	quietOps                                       int64
	settle, callDeadline                           time.Duration
}

func main() { os.Exit(realMain()) }

func realMain() int {
	var c config
	sizes := flag.String("sizes", "200,2000,5000", "room counts of the measured users")
	flag.StringVar(&c.mode, "mode", "all", "seed (seed and save the layout), measure (load the layout and measure -sizes) or all")
	flag.StringVar(&c.layoutPath, "layout", "bin/bench/roomlistbench-layout.json", "file the seed step writes and the measure step reads")
	flag.StringVar(&c.redis, "redis", cmp.Or(os.Getenv("REDIS_ADDR"), "chatim-redis:6379"), "Redis holding slot leases (env REDIS_ADDR)")
	flag.StringVar(&c.redisPW, "redis-password", "", "password of that Redis; env REDIS_PASSWORD when empty")
	flag.StringVar(&c.tenant, "tenant", "rlb", "tenant of the bench rooms")
	flag.StringVar(&c.core, "core", "chatim-core-1:9000", "core gRPC address every measured call goes to, so its caches are the ones measured")
	flag.StringVar(&c.metrics, "metrics", "http://chatim-core-1:9090/metrics", "admin /metrics of that core for room_cache_* deltas; empty skips")
	flag.StringVar(&c.mongoDB, "mongo-db", "chatim", "database of the cores")
	flag.IntVar(&c.perSize, "users-per-size", 20, "users per room count; each has its own private rooms, so each first call is cold")
	flag.IntVar(&c.private, "private", 40, "private rooms per user with the newest messages (first and next page)")
	flag.IntVar(&c.privateMessages, "private-messages", 5, "messages per private room, sent by the peer, so they are unread")
	flag.IntVar(&c.backgroundMessages, "background-messages", 1, "messages per shared background room, sent before the private ones")
	flag.IntVar(&c.page, "page", 20, "ListMyRooms limit")
	flag.IntVar(&c.repeats, "repeats", 5, "warm first and next page calls per user after its cold pair")
	flag.IntVar(&c.workers, "workers", 4, "parallel seeding calls")
	flag.IntVar(&c.joinMax, "join-max", 2000, "ROOM_LIST_JOIN_MAX of the cores, only to label the path")
	flag.Int64Var(&c.quietOps, "quiet-ops", 40, "measure only once Mongo runs below this many ops/s for 3s")
	flag.DurationVar(&c.settle, "settle", 15*time.Minute, "wait at most this long for room activity and a quiet Mongo")
	flag.DurationVar(&c.callDeadline, "call-deadline", 2*time.Minute, "total deadline of one seeding call across its retries (same request_id and cid)")
	flag.Parse()
	var err error
	if c.sizes, err = parseSizes(*sizes); err == nil {
		err = c.validate()
	}
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = run(ctx, c)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "roomlistbench:", err)
		return 1
	}
	return 0
}

func parseSizes(s string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("bad size %q in -sizes", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func (c config) validate() error {
	switch {
	case c.mode != "seed" && c.mode != "measure" && c.mode != "all":
		return fmt.Errorf("unknown -mode %q", c.mode)
	case c.perSize < 1 || c.page < 1 || c.page > 100 || c.privateMessages < 1 || c.workers < 1 || c.repeats < 1 || c.backgroundMessages < 0:
		return errors.New("-users-per-size, -private-messages, -workers and -repeats must be positive, -background-messages not negative, -page in 1..100")
	case c.private < 2*c.page:
		return errors.New("-private must hold two pages")
	}
	for _, s := range c.sizes {
		if s <= c.private {
			return fmt.Errorf("size %d must exceed -private %d", s, c.private)
		}
	}
	return nil
}

func run(ctx context.Context, c config) error {
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		return errors.New("MONGO_URI is not set (make poc passes it)")
	}
	probe, err := openProbe(ctx, uri, c.mongoDB)
	if err != nil {
		return err
	}
	defer probe.close()
	if c.mode != "measure" {
		if err := seedAndSave(ctx, probe, c); err != nil {
			return err
		}
	}
	if c.mode == "seed" {
		return nil
	}
	users, err := loadLayout(c.layoutPath)
	if err != nil {
		return err
	}
	return measureAll(ctx, probe, c, users)
}

func seedAndSave(ctx context.Context, probe *mongoProbe, c config) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s, err := route.Open(ctx, route.SessionConfig{
		RedisAddr: c.redis, RedisPassword: cmp.Or(c.redisPW, os.Getenv("REDIS_PASSWORD")), ClientName: "chatim-roomlistbench", Log: log,
		Policy: route.Policy{Deadline: c.callDeadline, Attempt: 10 * time.Second, MaxDelay: 2 * time.Second},
	})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	var b [4]byte
	_, _ = rand.Read(b[:])
	l, err := seed(ctx, s.Client, probe, c, "rl"+hex.EncodeToString(b[:]))
	if err != nil {
		return err
	}
	return saveLayout(c.layoutPath, l.users)
}

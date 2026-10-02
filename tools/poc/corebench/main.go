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
	"path/filepath"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/route"
	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
)

var coreKnobs = []string{
	"FLUSH_SHARDS", "FLUSH_WINDOW", "FLUSH_MAX_BATCH", "FLUSH_QUEUE", "FLUSH_INSERT_TIMEOUT",
	"ACTOR_MAX_GROUP", "ACTOR_MAILBOX", "CORE_REQUEST_DEADLINE", "CORE_MAX_INFLIGHT", "CORE_QUEUE_WAIT",
}

type config struct {
	redis, nats, tenant, texts, liveRoot string
	cpuProfile                           string
	redisDB, rooms, members, workers     int
	watch                                int
	zipf                                 float64
	deadline, attempt, grace             time.Duration
	plan                                 openloop.Plan
}

func main() { os.Exit(realMain()) }

func realMain() int {
	var c config
	flag.StringVar(&c.redis, "redis", cmp.Or(os.Getenv("REDIS_ADDR"), "chatim-redis:6379"), "Redis holding slot leases (env REDIS_ADDR)")
	flag.IntVar(&c.redisDB, "redis-db", 0, "Redis database index of the cores")
	flag.StringVar(&c.nats, "nats", cmp.Or(os.Getenv("NATS_URL"), "nats://chatim-nats:4222"), "NATS URL for -watch (env NATS_URL)")
	flag.StringVar(&c.liveRoot, "live-root", "live", "live subject root, EVT_LIVE_ROOT of the cores")
	flag.StringVar(&c.tenant, "tenant", "bench", "tenant of the bench rooms")
	flag.IntVar(&c.rooms, "rooms", 1000, "group rooms to create and send to")
	flag.IntVar(&c.members, "members", 4, "members per room, the creator included; each send comes from one of them")
	flag.IntVar(&c.workers, "setup-workers", 32, "rooms created in parallel")
	flag.IntVar(&c.plan.Rate, "rate", 10_000, "scheduled sends per second")
	flag.DurationVar(&c.plan.Duration, "duration", 60*time.Second, "measured window")
	flag.DurationVar(&c.plan.Warmup, "warmup", 5*time.Second, "sends before the measured window, excluded from stats")
	flag.IntVar(&c.plan.MaxInflight, "max-inflight", 4096, "sends in flight at most; a send due while full is shed by the client")
	flag.Float64Var(&c.zipf, "zipf", 0, "zipf exponent (>1) for room popularity; 0 picks rooms uniformly")
	flag.StringVar(&c.texts, "texts", "", "file with one message text per line; synthetic text when empty")
	flag.DurationVar(&c.deadline, "deadline", 10*time.Second, "give up one send after this long, retries included")
	flag.DurationVar(&c.attempt, "attempt", 5*time.Second, "timeout of one attempt")
	flag.IntVar(&c.watch, "watch", 0, "subscribe to the live subjects of the first N rooms (the hottest with -zipf) and measure event lag")
	flag.DurationVar(&c.grace, "watch-grace", 5*time.Second, "after the last send, wait this long for missing live events")
	flag.StringVar(&c.cpuProfile, "cpuprofile", "", "write a CPU profile of corebench itself to this file")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		fmt.Fprintln(os.Stderr, "corebench:", err)
		return 1
	}
	return 0
}

func (c config) validate() error {
	if err := c.plan.Validate(); err != nil {
		return err
	}
	switch {
	case c.rooms <= 0 || c.members <= 0 || c.workers <= 0:
		return errors.New("-rooms, -members and -setup-workers must be positive")
	case c.watch < 0 || c.watch > c.rooms:
		return errors.New("-watch must be in 0..rooms")
	case c.deadline <= 0 || c.attempt <= 0 || c.grace < 0:
		return errors.New("-deadline and -attempt must be positive, -watch-grace not negative")
	}
	return nil
}

func run(ctx context.Context, c config) error {
	if err := c.validate(); err != nil {
		return err
	}
	texts, err := loadTexts(c.texts)
	if err != nil {
		return err
	}
	if c.cpuProfile != "" {
		stopProfile, err := startProfile(c.cpuProfile)
		if err != nil {
			return err
		}
		defer stopProfile()
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s, err := route.Open(ctx, route.SessionConfig{
		RedisAddr: c.redis, RedisDB: c.redisDB, ClientName: "chatim-corebench", Log: log,
		Policy: route.Policy{Deadline: c.deadline, Attempt: c.attempt},
	})
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	printConfig(c, len(texts), slotShare(s))
	b := &bench{client: s.Client, tenant: c.tenant, run: runID(), texts: texts}
	started := time.Now()
	if b.rooms, err = createRooms(ctx, b.client, c, b.run); err != nil {
		return err
	}
	fmt.Printf("setup: %d rooms with %d members each in %v\n", c.rooms, c.members, time.Since(started).Round(time.Millisecond))
	if b.pick, err = openloop.NewPicker(c.rooms, c.zipf, uint64(time.Now().UnixNano())); err != nil {
		return err
	}
	var watch *liveWatch
	if c.watch > 0 {
		if watch, err = startWatch(c, b.rooms[:c.watch], b.run); err != nil {
			return err
		}
		defer watch.close()
		b.live, b.watched = watch.live, c.watch
	}
	counts := openloop.Run(ctx, c.plan, b.fire)
	openloop.Report{Plan: c.plan, Counts: counts, Sends: b.stats.Sends()}.Write(os.Stdout)
	if watch != nil {
		watch.await(ctx, c.grace)
		watch.report(os.Stdout)
	}
	return ctx.Err()
}

func printConfig(c config, texts int, share string) {
	fmt.Printf("corebench rate=%d/s duration=%v warmup=%v max-inflight=%d rooms=%d members=%d zipf=%v deadline=%v attempt=%v watch=%d texts=%d\n",
		c.plan.Rate, c.plan.Duration, c.plan.Warmup, c.plan.MaxInflight, c.rooms, c.members, c.zipf, c.deadline, c.attempt, c.watch, texts)
	knobs := make([]string, len(coreKnobs))
	for i, k := range coreKnobs {
		knobs[i] = k + "=" + cmp.Or(os.Getenv(k), "default")
	}
	fmt.Printf("core config declared for this run (corebench env, not read from the cores; default = core built-in): %s\n", strings.Join(knobs, " "))
	fmt.Printf("slot owners: %s\n", share)
}

func startProfile(path string) (func(), error) {
	f, err := os.Create(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("create cpu profile: %w", err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("start cpu profile: %w", err)
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}, nil
}

func runID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "cb" + hex.EncodeToString(b[:])
}

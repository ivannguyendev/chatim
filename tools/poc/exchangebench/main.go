package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
)

const usageText = `exchangebench: open-loop load on POST /v1/token of identity.

Every shot sends a freshly signed tenant token for one user of the set. Users are visited in a
random order that covers the whole set before any user repeats, so a first run against a new
issuer creates every user by JIT and a second run writes nothing. All tokens are signed before
the pacer starts so client signing time never shifts the schedule. Identity CPU and JIT writes
are read as deltas of the -metrics endpoint around the run.

Flags:
`

type config struct {
	url, metrics, alg, keyPath string
	issuer, audience, tenant   string
	userPrefix, usersFile      string
	users, signers             int
	ttl, timeout, readyWait    time.Duration
	plan                       openloop.Plan
}

func main() { os.Exit(realMain()) }

func realMain() int {
	var c config
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usageText)
		flag.PrintDefaults()
	}
	flag.StringVar(&c.url, "url", "http://chatim-identity-1:8080/v1/token", "token exchange endpoint")
	flag.StringVar(&c.metrics, "metrics", "http://chatim-identity-1:9090/metrics", "identity admin metrics endpoint")
	flag.StringVar(&c.alg, "alg", devtoken.AlgorithmES256, "algorithm of -key: ES256 or RS256 (EdDSA also works)")
	flag.StringVar(&c.keyPath, "key", "", "private JWK of the issuer key, written by corecli dev-issuer-key")
	flag.StringVar(&c.issuer, "issuer", "", "iss of every tenant token, registered with identity issuer-add")
	flag.StringVar(&c.audience, "audience", "chatim", "aud of every tenant token (IDENTITY_EXCHANGE_AUDIENCE)")
	flag.StringVar(&c.tenant, "tenant", "", "tid claim of every tenant token; empty sends none")
	flag.StringVar(&c.userPrefix, "user-prefix", "u", "user ids are this prefix plus 0..users-1")
	flag.IntVar(&c.users, "users", 100_000, "size of the user set")
	flag.StringVar(&c.usersFile, "users-file", "", "file with one existing user id per line; replaces -users and -user-prefix for the set (the probe still uses -user-prefix)")
	flag.IntVar(&c.signers, "signers", runtime.NumCPU(), "goroutines signing tokens before the run")
	flag.DurationVar(&c.ttl, "ttl", 9*time.Minute, "lifetime of each tenant token; must cover signing and the run, at most 10m")
	flag.DurationVar(&c.timeout, "timeout", 10*time.Second, "timeout of one request")
	flag.DurationVar(&c.readyWait, "ready-wait", 30*time.Second, "wait this long for the issuer key to load (one probe exchange per second)")
	flag.IntVar(&c.plan.Rate, "rate", 1700, "scheduled exchanges per second")
	flag.DurationVar(&c.plan.Duration, "duration", 60*time.Second, "measured window")
	flag.DurationVar(&c.plan.Warmup, "warmup", 5*time.Second, "exchanges before the measured window, excluded from latency and code counts")
	flag.IntVar(&c.plan.MaxInflight, "max-inflight", 2048, "exchanges in flight at most; a shot due while full is shed by the client")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		fmt.Fprintln(os.Stderr, "exchangebench:", err)
		return 1
	}
	return 0
}

func (c config) validate() error {
	if err := c.plan.Validate(); err != nil {
		return err
	}
	switch {
	case c.keyPath == "" || c.issuer == "":
		return errors.New("-key and -issuer are required")
	case c.users <= 0 || c.signers <= 0:
		return errors.New("-users and -signers must be positive")
	case c.ttl <= 0 || c.timeout <= 0 || c.readyWait <= 0:
		return errors.New("-ttl, -timeout and -ready-wait must be positive")
	}
	return nil
}

func run(ctx context.Context, c config) error {
	if err := c.validate(); err != nil {
		return err
	}
	key, err := loadKey(c.keyPath, c.alg)
	if err != nil {
		return err
	}
	ids, err := userSet(c)
	if err != nil {
		return err
	}
	fmt.Printf("exchangebench alg=%s kid=%s issuer=%s tenant=%q rate=%d/s duration=%v warmup=%v max-inflight=%d users=%d user-prefix=%q users-file=%q ttl=%v\n",
		key.Algorithm, key.KeyID, c.issuer, c.tenant, c.plan.Rate, c.plan.Duration, c.plan.Warmup, c.plan.MaxInflight, len(ids), c.userPrefix, c.usersFile, c.ttl)
	client := newExchangeClient(c.url, c.timeout, c.plan.MaxInflight)
	s := signer{key: key, cfg: c, ids: ids}
	waited, err := client.awaitReady(ctx, s, c.readyWait)
	if err != nil {
		return err
	}
	fmt.Printf("ready: probe exchange 200 after %v\n", waited.Round(time.Millisecond))
	started := time.Now()
	tokens, err := s.presign(ctx, c.plan.Total())
	if err != nil {
		return err
	}
	fmt.Printf("signed %d tenant tokens with %d goroutines in %v\n", len(tokens), c.signers, time.Since(started).Round(time.Millisecond))
	scraper := newScraper(c.metrics, c.timeout)
	before, err := scraper.scrape(ctx)
	if err != nil {
		return err
	}
	b := &bench{client: client, tokens: tokens}
	counts := openloop.Run(ctx, c.plan, b.fire)
	after, err := scraper.scrape(ctx)
	if err != nil {
		return err
	}
	b.stats.write(os.Stdout, c.plan, counts)
	writeDeltas(os.Stdout, before, after)
	return ctx.Err()
}

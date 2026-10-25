package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
	"github.com/ivannguyendev/chatim/tools/poc/internal/wsload"
)

const (
	scenarioSteady = "steady"
	scenarioKill   = "kill"
	scenarioBurst  = "burst"

	maxGroupMembers = 500
	fdHeadroom      = 256
)

const usageText = `wsload: load test the websocket nodes with real users, groups and messages.

Setup creates -groups groups through the gateway (each of the -conns distinct users is in one
group, one sender per group), signs a dev token per user with -keys and connects the users
spread evenly over the -ws URLs. A sender sends SendMessage over its websocket with a unique
cid; the text carries the send time, and every receiver records the delivery latency of
msg_created.

Scenarios:
  steady  connect at -connect-rate, open one group each, send -rate msg/s for -duration
  kill    connect, send at -rate, print kill-ready, wait for connections to drop, reconnect
          each after a random 0-3s to another URL (honouring retry_later) and measure the
          time from the first drop until every reconnected user is ready again
          (scripts/wsload-kill.sh kills chatim-websocket-2 on kill-ready)
  burst   dial and auth all -conns users at once; count retry_later and time to all ready

Limits: each websocket connection uses one file descriptor. Run with
  make poc TOOL=wsload POC_FLAGS="--ulimit nofile=65536:65536" ARGS="..."
The websocket nodes need the same limit (compose x-websocket sets ulimits nofile 65536).
On dev the tool shares the CPU with the nodes, the cores and the databases, so latency
numbers only validate the tool.

Results go to -out as summary.txt and summary.json.

Flags:
`

type config struct {
	urls        []string
	gateway     string
	keys        string
	tenant      string
	scenario    string
	out         string
	conns       int
	groups      int
	rate        int
	connectRate int
	duration    time.Duration
}

func main() { os.Exit(realMain()) }

func realMain() int {
	var c config
	var urls string
	flag.StringVar(&urls, "ws", "ws://chatim-websocket-1:8080/ws,ws://chatim-websocket-2:8080/ws", "comma-separated websocket node URLs; users are spread evenly over them")
	flag.StringVar(&c.gateway, "gateway", "http://chatim-gateway-1:8080", "HTTPS gateway used to create the groups")
	flag.StringVar(&c.keys, "keys", filepath.Join(".local", "ws-keys", devtoken.PrivateFile), "private dev JWK written by make dev-keys")
	flag.StringVar(&c.tenant, "tenant", "wsload", "tenant of the users and groups")
	flag.IntVar(&c.conns, "conns", 10_000, "connections, one distinct user each")
	flag.IntVar(&c.groups, "groups", 500, "groups; users are spread over them, one sender per group")
	flag.IntVar(&c.rate, "rate", 1000, "messages per second over all senders (kill: keep it low)")
	flag.IntVar(&c.connectRate, "connect-rate", 1000, "logins per second over all nodes in steady and kill; burst ignores it")
	flag.DurationVar(&c.duration, "duration", 60*time.Second, "steady: send window; kill: the longest wait for the first drop and for recovery")
	flag.StringVar(&c.scenario, "scenario", scenarioSteady, "steady, kill or burst")
	flag.StringVar(&c.out, "out", "", "result directory (default bin/bench/wsload-<scenario>-<timestamp>)")
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usageText)
		flag.PrintDefaults()
	}
	flag.Parse()
	c.urls = splitURLs(urls)
	if c.out == "" {
		c.out = filepath.Join("bin", "bench", "wsload-"+c.scenario+"-"+time.Now().Format("060102-150405"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		fmt.Fprintln(os.Stderr, "wsload:", err)
		return 1
	}
	return 0
}

func (c config) layout() wsload.Layout {
	return wsload.Layout{Users: c.conns, Groups: c.groups, Nodes: len(c.urls)}
}

func (c config) validate() error {
	switch {
	case len(c.urls) == 0:
		return errors.New("-ws needs at least one URL")
	case c.scenario != scenarioSteady && c.scenario != scenarioKill && c.scenario != scenarioBurst:
		return fmt.Errorf("-scenario %q: want steady, kill or burst", c.scenario)
	case c.rate <= 0 || c.connectRate <= 0 || c.duration <= 0:
		return errors.New("-rate, -connect-rate and -duration must be positive")
	case c.scenario == scenarioKill && len(c.urls) < 2:
		return errors.New("-scenario kill needs at least two -ws URLs")
	}
	return c.layout().Validate(maxGroupMembers)
}

func run(ctx context.Context, c config) error {
	if err := c.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(c.out, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", c.out, err)
	}
	warnFileLimit(c.conns)
	key, err := devtoken.LoadPrivateFile(c.keys)
	if err != nil {
		return err
	}
	l, err := newLoad(c, key, runID())
	if err != nil {
		return err
	}
	fmt.Printf("wsload scenario=%s run=%s conns=%d groups=%d members=%d rate=%d/s connect-rate=%d/s duration=%v ws=%s out=%s\n",
		c.scenario, l.run, c.conns, c.groups, c.layout().MembersOf(0), c.rate, c.connectRate, c.duration, strings.Join(c.urls, ","), c.out)
	started := time.Now()
	if err := l.createGroups(ctx); err != nil {
		return err
	}
	l.report.SetupSeconds = seconds(time.Since(started))
	fmt.Printf("setup: %d groups in %v\n", c.groups, time.Since(started).Round(time.Millisecond))
	runErr := l.runScenario(ctx)
	if err := l.writeSummary(); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

func splitURLs(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func warnFileLimit(conns int) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return
	}
	if want := uint64(conns + fdHeadroom); lim.Cur < want {
		fmt.Fprintf(os.Stderr, "wsload: open file limit %d is below %d; run with POC_FLAGS=\"--ulimit nofile=65536:65536\"\n", lim.Cur, want)
	}
}

func runID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "wl" + hex.EncodeToString(b[:])
}

func seconds(d time.Duration) float64 { return d.Seconds() }

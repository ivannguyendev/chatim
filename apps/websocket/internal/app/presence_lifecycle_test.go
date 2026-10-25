package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/presence"
	"github.com/ivannguyendev/chatim/pkg/wstest"
)

type presenceRedis struct {
	mr      *miniredis.Miniredis
	addr    string
	opts    atomic.Pointer[redis.Options]
	stamped chan struct{}
	closed  atomic.Int32
}

type countedPresenceConn struct {
	*redis.Client
	owner *presenceRedis
}

func newPresenceRedis(t *testing.T) *presenceRedis {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.RequireAuth(testPresencePassword)
	return &presenceRedis{mr: mr, addr: mr.Addr(), stamped: make(chan struct{}, 64)}
}

func (p *presenceRedis) open(opts *redis.Options) presence.Conn {
	p.opts.Store(opts)
	local := *opts
	local.Addr = p.addr
	return countedPresenceConn{Client: redis.NewClient(&local), owner: p}
}

func (c countedPresenceConn) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	cmds, err := c.Client.Pipelined(ctx, fn)
	if err == nil {
		select {
		case c.owner.stamped <- struct{}{}:
		default:
		}
	}
	return cmds, err
}

func (c countedPresenceConn) Close() error {
	c.owner.closed.Add(1)
	return c.Client.Close()
}

func TestRunStampsAConnectedUserAndClosesThePresenceRedisOnStop(t *testing.T) {
	mr := slotRedis(t)
	keys := newSigner(t)
	_, dial := startFakeCore(t)
	pres := newPresenceRedis(t)
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(ctx, testConfig(mr.Addr(), keys.path), logs.logger(), Deps{PresenceRedis: pres.open, Dial: dial, Listen: lis.listen, NATS: newFakeNATS().dial})
	adminAddr := awaitListener(t, lis.admin)
	wsAddr := awaitListener(t, lis.ws)
	logs.await(t, "websocket ready")
	opts := pres.opts.Load()
	if opts.Addr != "presence:6379" || opts.Password != testPresencePassword || opts.ClientName != "chatim-websocket-presence-ws-test" || opts.PoolSize != 4 {
		t.Fatalf("presence redis options addr %q client %q pool %d", opts.Addr, opts.ClientName, opts.PoolSize)
	}
	callCtx, callCancel := context.WithTimeout(context.Background(), waitLimit)
	defer callCancel()
	client, err := wstest.Dial(callCtx, "ws://"+wsAddr+"/ws")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := client.Auth(keys.token(t, time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("auth: %v", err)
	}
	defer func() { _ = client.Close() }()
	receive(t, pres.stamped, "presence stamp")
	if got, err := pres.mr.Get(presence.Key("t1", "u1")); err != nil || got != "1" {
		t.Fatalf("presence key = %q, %v; want 1", got, err)
	}
	if ttl := pres.mr.TTL(presence.Key("t1", "u1")); ttl <= 0 || ttl > 15*time.Second {
		t.Fatalf("presence ttl = %v", ttl)
	}
	_, body := httpStatus(t, adminAddr, "/metrics")
	for _, want := range []string{"chatim_websocket_presence_redis_up 1", "chatim_websocket_presence_stamps_total 1", "chatim_websocket_presence_stamp_failures_total 0", "chatim_websocket_presence_stamp_pending 0",
		"chatim_websocket_presence_reads_total 0", "chatim_websocket_presence_read_failures_total 0", "chatim_websocket_presence_cached_users 0",
		"chatim_websocket_presence_read_seconds_count 0", "chatim_websocket_presence_answers_total 0", "chatim_websocket_presence_watch_truncated_total 0",
		"chatim_websocket_presence_watching_connections 0", `chatim_websocket_presence_answer_dropped_total{reason="busy"} 0`,
		`chatim_websocket_presence_answer_dropped_total{reason="send_full"} 0`} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := pres.closed.Load(); got != 1 {
		t.Fatalf("presence redis closed %d times, want 1", got)
	}
}

func TestRunReadiesWithoutThePresenceRedis(t *testing.T) {
	mr := slotRedis(t)
	keys := newSigner(t)
	pres := newPresenceRedis(t)
	pres.mr.Close()
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(ctx, testConfig(mr.Addr(), keys.path), logs.logger(), Deps{PresenceRedis: pres.open, Listen: lis.listen, NATS: newFakeNATS().dial})
	adminAddr := awaitListener(t, lis.admin)
	logs.await(t, "websocket ready")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != 200 {
		t.Fatalf("/readyz = %d with the presence redis down, want 200", code)
	}
	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(logs.written(), testPresencePassword) {
		t.Fatal("a log line holds the presence redis password")
	}
}

func TestAFailedStartStopsTheStamperAndClosesThePresenceRedis(t *testing.T) {
	keys := newSigner(t)
	for name, deps := range map[string]func(*presenceRedis, *listeners) Deps{
		"slot table never loads": func(p *presenceRedis, l *listeners) Deps {
			return Deps{PresenceRedis: p.open, Listen: l.listen, ReadyTimeout: 300 * time.Millisecond, NATS: newFakeNATS().dial}
		},
		"admin address in use": func(p *presenceRedis, _ *listeners) Deps {
			return Deps{PresenceRedis: p.open, Listen: func(context.Context, string) (net.Listener, error) { return nil, errors.New("address in use") }}
		},
	} {
		t.Run(name, func(t *testing.T) {
			mr := slotRedis(t)
			mr.SetError("LOADING redis is loading the dataset")
			pres := newPresenceRedis(t)
			err := Run(t.Context(), testConfig(mr.Addr(), keys.path), newLogWatch().logger(), deps(pres, newListeners()))
			if err == nil {
				t.Fatal("Run = nil, want a start failure")
			}
			if got := pres.closed.Load(); got != 1 {
				t.Fatalf("presence redis closed %d times, want 1", got)
			}
		})
	}
}

func TestPresenceWiringCopiesTheConfigAndRedactsThePassword(t *testing.T) {
	cfg := testConfig("redis:6379", "/keys.json")
	pruned := 0
	got := presenceConfig(cfg, func(time.Time) { pruned++ })
	if got.TTL != cfg.PresenceTTL || got.StampEvery != cfg.PresenceStampEvery || got.Flush != cfg.PresenceFlush || got.Batch != cfg.PresenceBatch || got.Prune == nil {
		t.Fatalf("presence config = %+v from %+v", got, cfg)
	}
	if got.Prune(time.Now()); pruned != 1 {
		t.Fatalf("presence config prune ran %d times, want the given func", pruned)
	}
	redisCfg := presenceRedisConfig(cfg)
	if redisCfg.Addr != cfg.PresenceRedisAddr || redisCfg.DB != cfg.PresenceRedisDB || redisCfg.Pool != cfg.PresenceRedisPool ||
		redisCfg.Timeout != cfg.PresenceRedisTimeout || redisCfg.Password != testPresencePassword || redisCfg.Node != cfg.NodeID {
		t.Fatalf("presence redis config = %+v", redisCfg)
	}
	logs := newLogWatch()
	redactedLogger(logs.logger(), cfg).Error("presence redis failed", "err", errors.New("auth "+testPresencePassword+" refused"))
	if strings.Contains(logs.written(), testPresencePassword) {
		t.Fatal("the redacted logger let the presence redis password through")
	}
}

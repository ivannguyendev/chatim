package dedupe

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	degradedMsg  = "cid dedupe degraded to the local cache"
	recoveredMsg = "cid dedupe recovered"
	malformedMsg = "malformed cid dedupe value treated as absent"
)

const testTimeout = time.Second

var sampleRecord = Record{Seq: 7, Pts: 7, CreatedAt: time.UnixMilli(1_700_000_000_123).UTC()}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func newStore(t *testing.T, rdb *redis.Client, core string, sink *logSink) *Store {
	t.Helper()
	if sink == nil {
		sink = &logSink{}
	}
	s, err := New(rdb, Config{CoreID: core, Timeout: testTimeout}, slog.New(sink))
	if err != nil {
		t.Fatalf("New(%s): %v", core, err)
	}
	return s
}

func key(cid string) Key { return Key{Room: 42, User: "alice", CID: cid} }

func reserve(t *testing.T, s *Store, keys ...Key) []Verdict {
	t.Helper()
	got, err := s.Reserve(t.Context(), keys)
	if err != nil {
		t.Fatalf("Reserve(%v): %v", keys, err)
	}
	if len(got) != len(keys) {
		t.Fatalf("Reserve answered %d of %d keys", len(got), len(keys))
	}
	return got
}

func expectStatuses(t *testing.T, got []Verdict, want ...Status) {
	t.Helper()
	for i, v := range got {
		if v.Status != want[i] {
			t.Fatalf("verdict %d = %v, want %v (all: %v)", i, v.Status, want[i], got)
		}
	}
}

func expectValue(t *testing.T, mr *miniredis.Miniredis, k Key, want string) {
	t.Helper()
	got, err := mr.Get(k.String())
	if err != nil || got != want {
		t.Fatalf("value of %s = %q (%v), want %q", k, got, err, want)
	}
}

func sameRecord(a, b Record) bool {
	return a.Seq == b.Seq && a.Pts == b.Pts && a.CreatedAt.Equal(b.CreatedAt)
}

type logSink struct {
	mu   sync.Mutex
	msgs []string
}

func (s *logSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *logSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, r.Message)
	return nil
}

func (s *logSink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *logSink) WithGroup(string) slog.Handler { return s }

func (s *logSink) count(msg string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

type roundTrips struct{ n atomic.Int64 }

func countRoundTrips(rdb *redis.Client) *roundTrips {
	h := &roundTrips{}
	rdb.AddHook(h)
	return h
}

func (h *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmd)
	}
}

func (h *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.n.Add(1)
		return next(ctx, cmds)
	}
}

package eventmark_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMain(m *testing.M) {
	testlog.SilenceRedis()
	goleak.VerifyTestMain(m)
}

const room uint64 = 4242

func key(thread, seq uint64) store.MsgKey { return store.MsgKey{Room: room, Thread: thread, Seq: seq} }

func newStore(t *testing.T) (*miniredis.Miniredis, *eventmark.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = rdb.Close() })
	s, err := eventmark.New(rdb, eventmark.Config{TTL: time.Hour, Timeout: time.Second, Cooldown: time.Second}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mr, s
}

func TestMarkedKeysReadBackAsAcked(t *testing.T) {
	_, s := newStore(t)
	marked := []store.MsgKey{key(0, 1), key(0, 8192), key(3, 5)}
	if err := s.Mark(t.Context(), marked); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	got, err := s.Acked(t.Context(), append(slices.Clone(marked), key(0, 2), key(3, 1)))
	if err != nil {
		t.Fatalf("Acked: %v", err)
	}
	if want := []bool{true, true, true, false, false}; !slices.Equal(got, want) {
		t.Fatalf("Acked = %v, want %v", got, want)
	}
}

func TestOneKeyPerChunkWithTTL(t *testing.T) {
	mr, s := newStore(t)
	var keys []store.MsgKey
	for seq := uint64(1); seq <= 100; seq++ {
		keys = append(keys, key(0, seq))
	}
	keys = append(keys, key(0, 8192))
	if err := s.Mark(t.Context(), keys); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	want := []string{"chatim:evtack:4242:0:0", "chatim:evtack:4242:0:1"}
	if got := mr.Keys(); !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for _, k := range want {
		if ttl := mr.TTL(k); ttl <= 0 || ttl > time.Hour {
			t.Fatalf("TTL(%s) = %v, want within (0, 1h]", k, ttl)
		}
	}
}

func TestMarksExpire(t *testing.T) {
	mr, s := newStore(t)
	if err := s.Mark(t.Context(), []store.MsgKey{key(0, 1)}); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	mr.FastForward(time.Hour + time.Second)
	got, err := s.Acked(t.Context(), []store.MsgKey{key(0, 1)})
	if err != nil || got[0] {
		t.Fatalf("Acked after TTL = %v, %v; want [false], nil", got, err)
	}
}

func TestRedisDownIsAnError(t *testing.T) {
	mr, s := newStore(t)
	mr.Close()
	if _, err := s.Acked(t.Context(), []store.MsgKey{key(0, 1)}); err == nil {
		t.Fatal("Acked with redis down = nil error")
	}
	if err := s.Mark(t.Context(), []store.MsgKey{key(0, 1)}); err == nil {
		t.Fatal("Mark with redis down = nil error")
	}
}

func TestEmptyInputsSkipRedis(t *testing.T) {
	mr, s := newStore(t)
	mr.Close()
	if err := s.Mark(t.Context(), nil); err != nil {
		t.Fatalf("Mark(nil) = %v", err)
	}
	if got, err := s.Acked(t.Context(), nil); err != nil || got != nil {
		t.Fatalf("Acked(nil) = %v, %v", got, err)
	}
}

func TestConfigValidate(t *testing.T) {
	for _, c := range []eventmark.Config{{TTL: time.Millisecond}, {Timeout: -time.Second}, {Cooldown: -time.Second}} {
		if err := c.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("Validate(%+v) = %v, want ErrInvalidArgument", c, err)
		}
	}
	if err := (eventmark.Config{}).Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
}

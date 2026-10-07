package dedupe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func itRequestKeys(t *testing.T, rdb *redis.Client, ids ...string) []Key {
	t.Helper()
	var b [8]byte
	_, _ = rand.Read(b[:])
	room := binary.BigEndian.Uint64(b[:])>>1 | 1
	keys := make([]Key, len(ids))
	names := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = RequestKey(room, "it-user", id)
		names[i] = keys[i].String()
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rdb.Del(ctx, names...).Err(); err != nil {
			t.Errorf("clean up %v: %v", names, err)
		}
	})
	return keys
}

func TestRealRedisRequestLifecycle(t *testing.T) {
	if os.Getenv(itRedisDedupeAddrEnv) == "" {
		t.Skip("set CHATIM_IT_REDIS_DEDUPE_ADDR to run")
	}
	rdb := itClient(t)
	ctx := t.Context()
	const pending = 400 * time.Millisecond
	a := newRequests(t, itStore(t, rdb, "it-core-a", pending))
	b := newRequests(t, itStore(t, rdb, "it-core-b", pending))
	keys := itRequestKeys(t, rdb, "add", "dropped")
	add, dropped := keys[0], keys[1]

	expectBegin(t, a, add, RequestNew)
	if v := rdb.Get(ctx, add.String()).Val(); v != "p:it-core-a" {
		t.Fatalf("pending value = %q", v)
	}
	if ttl := rdb.PTTL(ctx, add.String()).Val(); ttl <= 0 || ttl > pending+ttlSlack {
		t.Fatalf("pending ttl = %v, want within (0, %v]", ttl, pending)
	}
	expectBegin(t, b, add, RequestBusy)

	rec := Record{Seq: 5, CreatedAt: time.Now()}
	a.Finish(ctx, add, rec)
	if v := rdb.Get(ctx, add.String()).Val(); v != committedValue(rec) {
		t.Fatalf("committed value = %q, want %q", v, committedValue(rec))
	}
	if ttl := rdb.PTTL(ctx, add.String()).Val(); ttl <= pending || ttl > DefaultCommittedTTL+ttlSlack {
		t.Fatalf("committed ttl = %v, want within (%v, %v]", ttl, pending, DefaultCommittedTTL)
	}
	expectBegin(t, b, add, RequestDone)
	restarted := newRequests(t, itStore(t, rdb, "it-core-a", pending))
	expectBegin(t, restarted, add, RequestDone)

	expectBegin(t, b, dropped, RequestNew)
	b.Cancel(ctx, dropped)
	if n := rdb.Exists(ctx, dropped.String()).Val(); n != 0 {
		t.Fatal("cancelled request kept its reservation")
	}
	expectBegin(t, a, dropped, RequestNew)
}

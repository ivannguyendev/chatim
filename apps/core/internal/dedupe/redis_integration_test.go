package dedupe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	itRedisAddrEnv = "CHATIM_IT_REDIS_ADDR"
	ttlSlack       = 10 * time.Millisecond
)

func itClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv(itRedisAddrEnv)
	if addr == "" {
		t.Skip("set CHATIM_IT_REDIS_ADDR to run")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("ping %s: %v", addr, err)
	}
	return rdb
}

func itKeys(t *testing.T, rdb *redis.Client, cids ...string) []Key {
	t.Helper()
	var b [8]byte
	_, _ = rand.Read(b[:])
	room := binary.BigEndian.Uint64(b[:])>>1 | 1
	keys := make([]Key, len(cids))
	names := make([]string, len(cids))
	for i, cid := range cids {
		keys[i] = Key{Room: room, User: "it-user", CID: cid}
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

func itStore(t *testing.T, rdb *redis.Client, core string, pending time.Duration) *Store {
	t.Helper()
	s, err := New(rdb, Config{CoreID: core, PendingTTL: pending}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New(%s): %v", core, err)
	}
	return s
}

func TestRealRedisReservationLifecycle(t *testing.T) {
	rdb := itClient(t)
	ctx := t.Context()
	const pending = 400 * time.Millisecond
	a, b := itStore(t, rdb, "it-core-a", pending), itStore(t, rdb, "it-core-b", pending)
	keys := itKeys(t, rdb, "x", "y", "z", "hash")
	x, y, z, hash := keys[0], keys[1], keys[2], keys[3]

	expectStatuses(t, reserve(t, a, x, y), Reserved, Reserved)
	if v := rdb.Get(ctx, x.String()).Val(); v != "p:it-core-a" {
		t.Fatalf("pending value = %q", v)
	}
	if ttl := rdb.PTTL(ctx, x.String()).Val(); ttl <= 0 || ttl > pending+ttlSlack {
		t.Fatalf("pending ttl = %v, want within (0, %v]", ttl, pending)
	}
	expectStatuses(t, reserve(t, a, x), PendingHere)
	expectStatuses(t, reserve(t, b, x, z), PendingElsewhere, Reserved)

	if err := a.Commit(ctx, []Entry{{Key: x, Record: sampleRecord}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got := reserve(t, b, x)
	if got[0].Status != Committed || !sameRecord(got[0].Record, sampleRecord) {
		t.Fatalf("after commit = %+v, want committed %+v", got[0], sampleRecord)
	}
	if ttl := rdb.PTTL(ctx, x.String()).Val(); ttl <= pending || ttl > DefaultCommittedTTL+ttlSlack {
		t.Fatalf("committed ttl = %v, want within (%v, %v]", ttl, pending, DefaultCommittedTTL)
	}

	if err := a.Abort(ctx, []Key{y, z}); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if n := rdb.Exists(ctx, y.String()).Val(); n != 0 {
		t.Fatal("own reservation survived Abort")
	}
	if v := rdb.Get(ctx, z.String()).Val(); v != "p:it-core-b" {
		t.Fatalf("Abort touched another core's reservation: %q", v)
	}

	if err := rdb.HSet(ctx, hash.String(), "f", "v").Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	expectStatuses(t, reserve(t, a, hash), Absent)

	time.Sleep(pending + 100*time.Millisecond)
	expectStatuses(t, reserve(t, a, z), Reserved)
}

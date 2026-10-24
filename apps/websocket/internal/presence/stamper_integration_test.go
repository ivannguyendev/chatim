package presence

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	itPresenceAddrEnv     = "CHATIM_IT_PRESENCE_REDIS_ADDR"
	itPresencePasswordEnv = "CHATIM_IT_PRESENCE_REDIS_PASSWORD"
	itWait                = 10 * time.Second
)

type signalStore struct {
	inner  Store
	rounds chan int
}

func (s *signalStore) Stamp(ctx context.Context, keys []string, ttl time.Duration) error {
	err := s.inner.Stamp(ctx, keys, ttl)
	if err == nil {
		s.rounds <- len(keys)
	}
	return err
}

type itRedis struct {
	t      *testing.T
	rdb    *redis.Client
	client *Client
	tenant string
	keys   []string
}

func (it *itRedis) touch(stamper *Stamper, user string) string {
	key := Key(it.tenant, user)
	it.keys = append(it.keys, key)
	stamper.Touch(it.tenant, user, time.Now())
	return key
}

func openITRedis(t *testing.T) *itRedis {
	t.Helper()
	addr := os.Getenv(itPresenceAddrEnv)
	if addr == "" {
		t.Skip("set " + itPresenceAddrEnv + " to run")
	}
	rdb := redis.NewClient(RedisOptions(RedisConfig{Addr: addr, Password: os.Getenv(itPresencePasswordEnv), Pool: 8, Timeout: testTimeout, Node: "itest"}))
	client, err := NewClient(rdb, testTimeout)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	it := &itRedis{t: t, rdb: rdb, client: client, tenant: "it" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	t.Cleanup(func() {
		if len(it.keys) > 0 {
			rdb.Del(context.WithoutCancel(t.Context()), it.keys...)
		}
		_ = client.Close()
	})
	return it
}

func (it *itRedis) start(cfg Config) (*Stamper, *signalStore) {
	it.t.Helper()
	store := &signalStore{inner: it.client, rounds: make(chan int, 64)}
	stamper, err := NewStamper(cfg, store)
	if err != nil {
		it.t.Fatalf("NewStamper: %v", err)
	}
	if err := stamper.Start(); err != nil {
		it.t.Fatalf("Start: %v", err)
	}
	it.t.Cleanup(stamper.Stop)
	return stamper, store
}

func (it *itRedis) awaitRound(store *signalStore) int {
	it.t.Helper()
	select {
	case n := <-store.rounds:
		return n
	case <-time.After(itWait):
		it.t.Fatal("no stamp round reached redis")
		return 0
	}
}

func TestStamperAgainstRedisWritesTheKeyWithTheTTL(t *testing.T) {
	it := openITRedis(t)
	stamper, store := it.start(testConfig(500))
	key := it.touch(stamper, "a")
	it.awaitRound(store)
	ctx := t.Context()
	if got, err := it.rdb.Get(ctx, key).Result(); err != nil || got != "1" {
		t.Fatalf("GET = %q, %v; want 1", got, err)
	}
	ttl, err := it.rdb.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 || ttl > testTTL {
		t.Fatalf("TTL = %v, %v; want in (0, %v]", ttl, err, testTTL)
	}
	it.touch(stamper, "a")
	select {
	case n := <-store.rounds:
		t.Fatalf("a touch inside the stamp interval wrote %d keys again", n)
	case <-time.After(2 * testFlush):
	}
	if got := it.client.Sources()[0].Read(); got != 1 {
		t.Fatalf("presence_redis_up = %v, want 1", got)
	}
}

func TestStamperAgainstRedisWritesManyUsersInBatches(t *testing.T) {
	it := openITRedis(t)
	stamper, store := it.start(testConfig(500))
	const users = 1200
	keys := make([]string, users)
	for i := range users {
		keys[i] = it.touch(stamper, fmt.Sprintf("u%d", i))
	}
	sizes := []int{it.awaitRound(store), it.awaitRound(store), it.awaitRound(store)}
	if sizes[0]+sizes[1]+sizes[2] != users || max(sizes[0], sizes[1], sizes[2]) != 500 {
		t.Fatalf("batches %v, want 1200 keys in batches of at most 500", sizes)
	}
	if n, err := it.rdb.Exists(t.Context(), keys...).Result(); err != nil || n != users {
		t.Fatalf("EXISTS = %d, %v; want %d", n, err, users)
	}
}

func TestStamperAgainstRedisLetsTheKeyExpireOnceTouchesStop(t *testing.T) {
	it := openITRedis(t)
	stamper, store := it.start(Config{TTL: time.Second, StampEvery: 200 * time.Millisecond, Flush: 100 * time.Millisecond, Batch: 500})
	key := it.touch(stamper, "a")
	it.awaitRound(store)
	time.Sleep(1500 * time.Millisecond)
	if n, err := it.rdb.Exists(t.Context(), key).Result(); err != nil || n != 0 {
		t.Fatalf("EXISTS = %d, %v after the ttl without a touch; want 0", n, err)
	}
	if got := stamper.pendingCount(); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
}

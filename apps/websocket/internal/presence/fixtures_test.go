package presence

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"
)

const (
	testTTL        = 15 * time.Second
	testStampEvery = 2 * time.Second
	testFlush      = time.Second
	testTimeout    = 500 * time.Millisecond
	testPassword   = "pr3s3nc3-s3cr3t"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func testConfig(batch int) Config {
	return Config{TTL: testTTL, StampEvery: testStampEvery, Flush: testFlush, Batch: batch}
}

type fixture struct {
	t       *testing.T
	mr      *miniredis.Miniredis
	client  *Client
	store   *countingStore
	stamper *Stamper
}

type countingStore struct {
	inner Store
	calls atomic.Int64
}

func (c *countingStore) Stamp(ctx context.Context, keys []string, ttl time.Duration) error {
	c.calls.Add(1)
	return c.inner.Stamp(ctx, keys, ttl)
}

func newMiniredis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.RequireAuth(testPassword)
	return mr
}

type outside struct {
	jobs chan func()
	done chan struct{}
}

func runOutside(t *testing.T) *outside {
	t.Helper()
	o := &outside{jobs: make(chan func()), done: make(chan struct{})}
	go func() {
		for job := range o.jobs {
			job()
			o.done <- struct{}{}
		}
	}()
	t.Cleanup(func() { close(o.jobs) })
	return o
}

func (o *outside) do(job func()) {
	o.jobs <- job
	<-o.done
}

func newClientTo(t *testing.T, addr string) *Client {
	t.Helper()
	client, err := NewClient(redis.NewClient(RedisOptions(RedisConfig{Addr: addr, Password: testPassword, Pool: 4, Timeout: testTimeout, Node: "ws-test"})), testTimeout)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func startFixture(t *testing.T, mr *miniredis.Miniredis, batch int) *fixture {
	t.Helper()
	client := newClientTo(t, mr.Addr())
	store := &countingStore{inner: client}
	stamper, err := NewStamper(testConfig(batch), store)
	if err != nil {
		t.Fatalf("NewStamper: %v", err)
	}
	if err := stamper.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f := &fixture{t: t, mr: mr, client: client, store: store, stamper: stamper}
	t.Cleanup(func() {
		stamper.Stop()
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return f
}

func (f *fixture) advance(d time.Duration) {
	f.t.Helper()
	time.Sleep(d)
	f.mr.FastForward(d)
}

func (f *fixture) step(total, every time.Duration, each func()) {
	f.t.Helper()
	for elapsed := time.Duration(0); elapsed < total; elapsed += every {
		f.advance(every)
		synctest.Wait()
		each()
	}
}

func (f *fixture) touch(user string) { f.stamper.Touch("t1", user, time.Now()) }

func (f *fixture) online(user string) bool { return f.mr.Exists(Key("t1", user)) }

func (f *fixture) metric(name string) float64 {
	f.t.Helper()
	for _, s := range append(f.stamper.Sources(), f.client.Sources()...) {
		if s.Name == name {
			return s.Read()
		}
	}
	f.t.Fatalf("no metric %s", name)
	return 0
}

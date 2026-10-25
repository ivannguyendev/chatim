package presence

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestARedisOutageStopsTheRoundAndRetriesTheRestOnceRedisIsBack(t *testing.T) {
	mr := newMiniredis(t)
	ctl := runOutside(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 1)
		f.advance(halfFlush)
		mr.Close()
		for _, u := range []string{"a", "b", "c"} {
			f.touch(u)
		}
		f.advance(testFlush)
		synctest.Wait()
		if f.store.calls.Load() != 1 || f.metric("presence_stamp_failures_total") != 1 {
			t.Fatalf("pipelines %d failures %v, want the round to stop at the first batch", f.store.calls.Load(), f.metric("presence_stamp_failures_total"))
		}
		if f.metric("presence_stamp_pending") != 3 || f.metric("presence_redis_up") != 0 {
			t.Fatalf("pending %v up %v, want 3 and 0", f.metric("presence_stamp_pending"), f.metric("presence_redis_up"))
		}
		var restarted error
		ctl.do(func() { restarted = mr.Restart() })
		if restarted != nil {
			t.Fatalf("Restart: %v", restarted)
		}
		f.advance(testFlush)
		synctest.Wait()
		for _, u := range []string{"a", "b", "c"} {
			if !f.online(u) {
				t.Fatalf("%s not stamped after redis came back", u)
			}
		}
		if f.metric("presence_stamps_total") != 3 || f.metric("presence_stamp_pending") != 0 || f.metric("presence_redis_up") != 1 {
			t.Fatalf("stamps %v pending %v up %v, want 3, 0 and 1", f.metric("presence_stamps_total"), f.metric("presence_stamp_pending"), f.metric("presence_redis_up"))
		}
	})
}

func TestAUserIdleForTheTTLIsNotStampedWhenRedisReturns(t *testing.T) {
	mr := newMiniredis(t)
	ctl := runOutside(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 500)
		f.advance(halfFlush)
		f.touch("a")
		f.touch("gone")
		f.advance(testFlush)
		synctest.Wait()
		mr.Close()
		f.step(testTTL+2*testFlush, testFlush, func() { f.touch("a") })
		if f.online("gone") {
			t.Fatal("the idle user's key outlived its ttl")
		}
		var restarted error
		ctl.do(func() { restarted = mr.Restart() })
		if restarted != nil {
			t.Fatalf("Restart: %v", restarted)
		}
		f.advance(2 * testFlush)
		synctest.Wait()
		if !f.online("a") || f.online("gone") {
			t.Fatalf("online a %v gone %v, want only the user still touching", f.online("a"), f.online("gone"))
		}
		if got := f.metric("presence_stamps_total"); got != 3 {
			t.Fatalf("stamps = %v, want 2 before the outage and 1 after", got)
		}
	})
}

type silentRedis struct {
	lis      net.Listener
	accepted chan struct{}
	mu       sync.Mutex
	conns    []net.Conn
	done     chan struct{}
}

func newSilentRedis(t *testing.T) *silentRedis {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &silentRedis{lis: lis, accepted: make(chan struct{}, 16), done: make(chan struct{})}
	go s.accept()
	t.Cleanup(s.close)
	return s
}

func (s *silentRedis) accept() {
	defer close(s.done)
	for {
		conn, err := s.lis.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		select {
		case s.accepted <- struct{}{}:
		default:
		}
	}
}

func (s *silentRedis) close() {
	_ = s.lis.Close()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
}

func TestAHungRedisBoundsTheRoundAndNeverBlocksTouch(t *testing.T) {
	silent := newSilentRedis(t)
	client := newClientTo(t, silent.lis.Addr().String())
	defer func() { _ = client.Close() }()
	stamper, err := NewStamper(testConfig(1), client)
	if err != nil {
		t.Fatalf("NewStamper: %v", err)
	}
	stamper.Touch("t1", "a", time.Now())
	stamper.Touch("t1", "b", time.Now())
	rounds := make(chan time.Duration, 1)
	go func() {
		began := time.Now()
		stamper.flush(context.Background())
		rounds <- time.Since(began)
	}()
	<-silent.accepted
	touched := make(chan struct{})
	go func() {
		stamper.Touch("t1", "c", time.Now())
		close(touched)
	}()
	select {
	case <-touched:
	case elapsed := <-rounds:
		t.Fatalf("Touch waited for the hung round of %v", elapsed)
	}
	elapsed := <-rounds
	if elapsed > 2*testTimeout+time.Second {
		t.Fatalf("round took %v, want about the %v redis timeout", elapsed, testTimeout)
	}
	if stamper.failures.Load() != 1 || stamper.pendingCount() != 3 {
		t.Fatalf("failures %d pending %d, want 1 and every user back in the pending set", stamper.failures.Load(), stamper.pendingCount())
	}
	if got := client.Sources()[0].Read(); got != 0 {
		t.Fatalf("presence_redis_up = %v after a hung round, want 0", got)
	}
}

func TestStopEndsAHungRoundWithinTheRedisTimeout(t *testing.T) {
	silent := newSilentRedis(t)
	client := newClientTo(t, silent.lis.Addr().String())
	defer func() { _ = client.Close() }()
	cfg := testConfig(500)
	cfg.Flush = 10 * time.Millisecond
	stamper, err := NewStamper(cfg, client)
	if err != nil {
		t.Fatalf("NewStamper: %v", err)
	}
	stamper.Touch("t1", "a", time.Now())
	if err := stamper.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-silent.accepted
	began := time.Now()
	stamper.Stop()
	if elapsed := time.Since(began); elapsed > testTimeout+time.Second {
		t.Fatalf("Stop took %v during a hung round", elapsed)
	}
}

func TestStamperLifecycleAndValidation(t *testing.T) {
	store := &countingStore{}
	for name, cfg := range map[string]Config{
		"zero ttl":         {StampEvery: time.Second, Flush: time.Second, Batch: 1},
		"zero stamp every": {TTL: time.Second, Flush: time.Second, Batch: 1},
		"zero flush":       {TTL: time.Second, StampEvery: time.Second, Batch: 1},
		"zero batch":       {TTL: time.Second, StampEvery: time.Second, Flush: time.Second},
		"batch over 1000":  {TTL: time.Second, StampEvery: time.Second, Flush: time.Second, Batch: 1001},
	} {
		if _, err := NewStamper(cfg, store); err == nil {
			t.Errorf("%s: NewStamper accepted %+v", name, cfg)
		}
	}
	if _, err := NewStamper(testConfig(1000), nil); !errors.Is(err, errNoStore) {
		t.Fatalf("NewStamper without a store = %v", err)
	}
	stamper, err := NewStamper(testConfig(1000), store)
	if err != nil {
		t.Fatalf("NewStamper at batch 1000: %v", err)
	}
	stamper.Stop()
	if err := stamper.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := stamper.Start(); !errors.Is(err, errStartedTwice) {
		t.Fatalf("second Start = %v", err)
	}
	stamper.Stop()
	stamper.Stop()
}

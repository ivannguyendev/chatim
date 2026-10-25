package presence

import (
	"testing"
	"testing/synctest"
	"time"
)

const halfFlush = testFlush / 2

func TestTheFirstTouchIsStampedWithinOneFlush(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 500)
		f.touch("a")
		f.advance(testFlush)
		synctest.Wait()
		if !f.online("a") {
			t.Fatal("key missing one flush after the first touch")
		}
		if ttl := mr.TTL(Key("t1", "a")); ttl <= 0 || ttl > testTTL {
			t.Fatalf("ttl = %v, want in (0, %v]", ttl, testTTL)
		}
		if got, _ := mr.Get(Key("t1", "a")); got != "1" {
			t.Fatalf("value = %q, want 1", got)
		}
		if f.metric("presence_stamps_total") != 1 || f.metric("presence_redis_up") != 1 || f.metric("presence_stamp_pending") != 0 {
			t.Fatalf("stamps %v up %v pending %v, want 1, 1 and 0", f.metric("presence_stamps_total"), f.metric("presence_redis_up"), f.metric("presence_stamp_pending"))
		}
	})
}

func TestAHeartbeatEverySecondStampsOnceEveryTwoSeconds(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 500)
		f.advance(halfFlush)
		f.touch("a")
		f.advance(testFlush)
		synctest.Wait()
		before := mr.CommandCount()
		f.step(10*time.Second, time.Second, func() { f.touch("a") })
		if got := mr.CommandCount() - before; got != 5 {
			t.Fatalf("redis commands over 10s of 1s heartbeats = %d, want 5", got)
		}
		if got := f.metric("presence_stamps_total"); got != 6 {
			t.Fatalf("stamps = %v, want 6", got)
		}
	})
}

func TestTwoConnectionsOfOneUserDoNotDoubleTheWrites(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 500)
		f.advance(halfFlush)
		f.touch("a")
		f.advance(testFlush)
		synctest.Wait()
		before := mr.CommandCount()
		f.step(10*time.Second, halfFlush, func() { f.touch("a") })
		if got := mr.CommandCount() - before; got != 5 {
			t.Fatalf("redis commands over 10s with two connections = %d, want 5", got)
		}
	})
}

func TestSparseHeartbeatsKeepTheKeyAlive(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 500)
		start := time.Now()
		f.touch("a")
		touches := []time.Duration{4900 * time.Millisecond, 14900 * time.Millisecond}
		f.step(25*time.Second, 100*time.Millisecond, func() {
			elapsed := time.Since(start)
			if len(touches) > 0 && elapsed >= touches[0] {
				f.touch("a")
				touches = touches[1:]
			}
			if elapsed >= testFlush && !f.online("a") {
				t.Fatalf("key expired at %v between heartbeats", elapsed)
			}
		})
	})
}

func TestTheKeyExpiresOnceTouchesStop(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 500)
		f.advance(halfFlush)
		f.touch("a")
		f.advance(testFlush)
		synctest.Wait()
		f.advance(testTTL - 2*testFlush)
		synctest.Wait()
		if !f.online("a") {
			t.Fatal("key expired before the ttl")
		}
		f.advance(2 * testFlush)
		synctest.Wait()
		if f.online("a") {
			t.Fatal("key still there after the ttl without a touch")
		}
		if f.metric("presence_stamps_total") != 1 || f.metric("presence_stamp_pending") != 0 {
			t.Fatalf("stamps %v pending %v, want 1 and 0", f.metric("presence_stamps_total"), f.metric("presence_stamp_pending"))
		}
	})
}

func TestManyUsersAreWrittenInBatches(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := startFixture(t, mr, 2)
		for _, u := range []string{"a", "b", "c", "d", "e"} {
			f.touch(u)
		}
		f.advance(testFlush)
		synctest.Wait()
		if got := f.store.calls.Load(); got != 3 {
			t.Fatalf("pipelines = %d, want 3 batches for 5 users of batch 2", got)
		}
		for _, u := range []string{"a", "b", "c", "d", "e"} {
			if !f.online(u) {
				t.Fatalf("%s not stamped", u)
			}
		}
	})
}

func TestStampsUseTheTenantInTheKey(t *testing.T) {
	if got := Key("t1", "alice"); got != "chatim:presence:t1:alice" {
		t.Fatalf("Key = %q", got)
	}
}

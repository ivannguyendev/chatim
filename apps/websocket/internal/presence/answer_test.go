package presence

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alicebob/miniredis/v2"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const testCache = 5 * time.Second

type answerFixture struct {
	t        *testing.T
	mr       *miniredis.Miniredis
	client   *Client
	answerer *Answerer
}

func newAnswerFixture(t *testing.T, mr *miniredis.Miniredis) *answerFixture {
	t.Helper()
	client := newClientTo(t, mr.Addr())
	t.Cleanup(func() { _ = client.Close() })
	answerer, err := NewAnswerer(testCache, client)
	if err != nil {
		t.Fatalf("NewAnswerer: %v", err)
	}
	f := &answerFixture{t: t, mr: mr, client: client, answerer: answerer}
	f.ask("t1", "warm-up")
	return f
}

func (f *answerFixture) ask(tenant string, users ...string) map[string]bool {
	f.t.Helper()
	entries, err := f.answerer.Answer(context.Background(), tenant, users)
	if err != nil {
		f.t.Fatalf("Answer: %v", err)
	}
	return onlineOf(f.t, users, entries)
}

func onlineOf(t *testing.T, users []string, entries []*chatimv1.PresenceEntry) map[string]bool {
	t.Helper()
	if len(entries) != len(users) {
		t.Fatalf("entries = %d, want one per user %v", len(entries), users)
	}
	out := map[string]bool{}
	for i, e := range entries {
		if e.GetUserId() != users[i] {
			t.Fatalf("entry %d = %q, want %q in list order", i, e.GetUserId(), users[i])
		}
		out[e.GetUserId()] = e.GetOnline()
	}
	return out
}

func (f *answerFixture) online(user string) {
	f.t.Helper()
	if err := f.mr.Set(Key("t1", user), onlineValue); err != nil {
		f.t.Fatalf("set %s: %v", user, err)
	}
}

func (f *answerFixture) metric(name string) float64 {
	f.t.Helper()
	for _, s := range append(f.answerer.Sources(), f.client.Sources()...) {
		if s.Name == name {
			if s.Histogram != nil {
				return float64(s.Histogram().Count)
			}
			return s.Read()
		}
	}
	f.t.Fatalf("no metric %s", name)
	return 0
}

func (f *answerFixture) advance(d time.Duration) {
	time.Sleep(d)
	f.mr.FastForward(d)
}

func TestAnswerReturnsTheWholeListInOrder(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := newAnswerFixture(t, mr)
		f.online("lan")
		got := f.ask("t1", "lan", "nam")
		if !got["lan"] || got["nam"] {
			t.Fatalf("answer = %v, want lan online and nam offline", got)
		}
		if f.metric("presence_reads_total") != 2 || f.metric("presence_read_seconds") != 2 || f.metric("presence_redis_up") != 1 {
			t.Fatalf("reads %v observed %v up %v, want 2, 2 and 1", f.metric("presence_reads_total"), f.metric("presence_read_seconds"), f.metric("presence_redis_up"))
		}
	})
}

func TestAnOnlineUserIsReadOnceForEveryAskWithinTheCacheTime(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := newAnswerFixture(t, mr)
		f.online("lan")
		before := mr.CommandCount()
		for range 100 {
			if !f.ask("t1", "lan")["lan"] {
				t.Fatal("lan answered offline")
			}
			f.advance(49 * time.Millisecond)
		}
		if got := mr.CommandCount() - before; got != 1 {
			t.Fatalf("redis commands for 100 asks within %v = %d, want 1", testCache, got)
		}
		f.advance(testCache - 100*49*time.Millisecond)
		if !f.ask("t1", "lan")["lan"] {
			t.Fatal("lan answered offline after the cache time")
		}
		if got := mr.CommandCount() - before; got != 2 {
			t.Fatalf("redis commands after the cache time = %d, want 2", got)
		}
	})
}

func TestAnOfflineUserIsReadOnEveryAskAndShowsOnlineAtOnce(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := newAnswerFixture(t, mr)
		before := mr.CommandCount()
		for range 3 {
			if f.ask("t1", "nam")["nam"] {
				t.Fatal("nam answered online without a key")
			}
			f.advance(time.Second)
		}
		if got := mr.CommandCount() - before; got != 3 {
			t.Fatalf("redis commands for 3 offline asks = %d, want 3", got)
		}
		f.online("nam")
		if !f.ask("t1", "nam")["nam"] {
			t.Fatal("nam answered offline right after the key appeared")
		}
		if got := f.metric("presence_cached_users"); got != 1 {
			t.Fatalf("cached users = %v, want only nam", got)
		}
	})
}

func TestAnExpiredKeyShowsOfflineOnceTheCacheEntryIsOld(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := newAnswerFixture(t, mr)
		f.online("lan")
		f.ask("t1", "lan")
		f.advance(time.Second)
		mr.Del(Key("t1", "lan"))
		f.advance(testCache - time.Second - time.Millisecond)
		if !f.ask("t1", "lan")["lan"] {
			t.Fatal("lan answered offline while its cache entry was fresh")
		}
		f.advance(time.Millisecond)
		if f.ask("t1", "lan")["lan"] {
			t.Fatal("lan answered online after its cache entry got old and its key was gone")
		}
		if got := f.metric("presence_cached_users"); got != 0 {
			t.Fatalf("cached users = %v, want the offline read to drop lan", got)
		}
	})
}

func TestAnotherTenantDoesNotSeeTheKeyOfTheSameUserID(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := newAnswerFixture(t, mr)
		f.online("lan")
		if !f.ask("t1", "lan")["lan"] {
			t.Fatal("lan of t1 answered offline")
		}
		if f.ask("t2", "lan")["lan"] {
			t.Fatal("lan of t2 answered online from the key or cache of t1")
		}
	})
}

func TestEntriesOlderThanTheCacheTimeArePrunedByTheStamperRound(t *testing.T) {
	mr := newMiniredis(t)
	synctest.Test(t, func(t *testing.T) {
		f := newAnswerFixture(t, mr)
		cfg := testConfig(500)
		cfg.Prune = f.answerer.Prune
		stamper, err := NewStamper(cfg, f.client)
		if err != nil {
			t.Fatalf("NewStamper: %v", err)
		}
		if err := stamper.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer stamper.Stop()
		f.advance(testFlush / 2)
		f.online("lan")
		f.online("an")
		f.ask("t1", "lan", "an")
		f.advance(testCache)
		synctest.Wait()
		if got := f.metric("presence_cached_users"); got != 2 {
			t.Fatalf("cached users = %v before the cache time, want 2", got)
		}
		f.advance(testFlush)
		synctest.Wait()
		if got := f.metric("presence_cached_users"); got != 0 {
			t.Fatalf("cached users = %v after the cache time, want 0", got)
		}
	})
}

func TestARedisOutageAnswersNothingAndRecovers(t *testing.T) {
	mr := newMiniredis(t)
	ctl := runOutside(t)
	synctest.Test(t, func(t *testing.T) {
		f := newAnswerFixture(t, mr)
		f.online("lan")
		mr.Close()
		if entries, err := f.answerer.Answer(context.Background(), "t1", []string{"lan"}); err == nil || entries != nil {
			t.Fatalf("Answer during an outage = %v, %v; want no entries and an error", entries, err)
		}
		if f.metric("presence_read_failures_total") != 1 || f.metric("presence_redis_up") != 0 || f.metric("presence_cached_users") != 0 {
			t.Fatalf("failures %v up %v cached %v, want 1, 0 and 0", f.metric("presence_read_failures_total"), f.metric("presence_redis_up"), f.metric("presence_cached_users"))
		}
		var restarted error
		ctl.do(func() { restarted = mr.Restart() })
		if restarted != nil {
			t.Fatalf("Restart: %v", restarted)
		}
		f.online("lan")
		if !f.ask("t1", "lan")["lan"] || f.metric("presence_redis_up") != 1 {
			t.Fatal("lan not online after redis came back")
		}
	})
}

func TestAHungRedisEndsTheReadWithinTheRedisTimeout(t *testing.T) {
	silent := newSilentRedis(t)
	client := newClientTo(t, silent.lis.Addr().String())
	defer func() { _ = client.Close() }()
	answerer, err := NewAnswerer(testCache, client)
	if err != nil {
		t.Fatalf("NewAnswerer: %v", err)
	}
	began := time.Now()
	if _, err := answerer.Answer(context.Background(), "t1", []string{"lan"}); err == nil {
		t.Fatal("Answer against a hung redis succeeded")
	}
	if elapsed := time.Since(began); elapsed > 2*testTimeout+time.Second {
		t.Fatalf("read took %v, want about the %v redis timeout", elapsed, testTimeout)
	}
	if answerer.failures.Load() != 1 {
		t.Fatalf("failures = %d, want 1", answerer.failures.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := answerer.Answer(ctx, "t1", []string{"lan"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Answer with a cancelled context = %v", err)
	}
	if answerer.failures.Load() != 1 {
		t.Fatalf("failures = %d after a cancelled read, want it not counted", answerer.failures.Load())
	}
}

func TestNewAnswererValidation(t *testing.T) {
	if _, err := NewAnswerer(testCache, nil); !errors.Is(err, errNoReader) {
		t.Fatalf("NewAnswerer without a reader = %v", err)
	}
	if _, err := NewAnswerer(0, &Client{}); !errors.Is(err, errBadCache) {
		t.Fatalf("NewAnswerer with a zero cache time = %v", err)
	}
}

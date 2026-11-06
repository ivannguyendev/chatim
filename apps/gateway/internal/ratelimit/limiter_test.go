package ratelimit_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/ratelimit"
)

func ownerLimits() ratelimit.Config {
	return ratelimit.Config{
		Command:    "GetPartURLs",
		User:       ratelimit.Limit{Count: 2, Window: time.Second},
		UserIP:     ratelimit.Limit{Count: 1, Window: time.Second},
		Device:     ratelimit.Limit{Count: 1, Window: 5 * time.Second},
		LocalUsers: 1000,
	}
}

func newLimiter(t *testing.T) (*ratelimit.Limiter, *miniredis.Miniredis, *time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	l, err := ratelimit.New(rdb, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.UnixMilli(1_790_000_000_000)
	l.SetClock(func() time.Time { return clock })
	return l, mr, &clock
}

func allow(t *testing.T, l *ratelimit.Limiter, k ratelimit.Key) ratelimit.Decision {
	t.Helper()
	d, err := l.Allow(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDeviceGetsOneCallPerFiveSeconds(t *testing.T) {
	l, mr, clock := newLimiter(t)
	phone := ratelimit.Key{Tenant: "acme", User: "an", IP: "10.0.0.1", Device: "s1"}
	if d := allow(t, l, phone); !d.Allowed {
		t.Fatalf("first call %+v", d)
	}
	if d := allow(t, l, phone); d.Allowed || d.Tier != ratelimit.TierDevice {
		t.Fatalf("second call %+v", d)
	}
	*clock = clock.Add(5 * time.Second)
	mr.FastForward(5 * time.Second)
	if d := allow(t, l, phone); !d.Allowed {
		t.Fatalf("after 5s %+v", d)
	}
}

func TestUserAndIPTiers(t *testing.T) {
	l, _, clock := newLimiter(t)
	*clock = clock.Truncate(5 * time.Second)
	if d := allow(t, l, ratelimit.Key{Tenant: "acme", User: "an", IP: "10.0.0.1", Device: "s1"}); !d.Allowed {
		t.Fatalf("phone %+v", d)
	}
	if d := allow(t, l, ratelimit.Key{Tenant: "acme", User: "an", IP: "10.0.0.1", Device: "s2"}); d.Allowed || d.Tier != ratelimit.TierUserIP {
		t.Fatalf("laptop on the same IP %+v", d)
	}
	if d := allow(t, l, ratelimit.Key{Tenant: "acme", User: "an", IP: "10.0.0.2", Device: "s3"}); d.Allowed || d.Tier != ratelimit.TierUser {
		t.Fatalf("third call of the user in one second %+v", d)
	}
	if d := allow(t, l, ratelimit.Key{Tenant: "acme", User: "binh", IP: "10.0.0.1", Device: "s9"}); !d.Allowed {
		t.Fatalf("another user %+v", d)
	}
	got := map[string]uint64{}
	for _, c := range l.Limited() {
		got[c.Tier] = c.Read()
	}
	if got[ratelimit.TierUserIP] != 1 || got[ratelimit.TierUser] != 1 || got[ratelimit.TierDevice] != 0 {
		t.Fatalf("limited counts %v", got)
	}
}

func TestKeysExpireWithTheirWindow(t *testing.T) {
	l, mr, _ := newLimiter(t)
	allow(t, l, ratelimit.Key{Tenant: "acme", User: "an", IP: "10.0.0.1", Device: "s1"})
	if n := len(mr.Keys()); n != 3 {
		t.Fatalf("keys %v", mr.Keys())
	}
	mr.FastForward(5 * time.Second)
	if n := len(mr.Keys()); n != 0 {
		t.Fatalf("keys left after the longest window: %v", mr.Keys())
	}
}

func TestRedisDownFallsBackToCountingOnThisNode(t *testing.T) {
	l, mr, _ := newLimiter(t)
	mr.Close()
	phone := ratelimit.Key{Tenant: "acme", User: "an", IP: "10.0.0.1", Device: "s1"}
	first, err := l.Allow(t.Context(), phone)
	if err == nil || !first.Allowed {
		t.Fatalf("first decision %+v err %v", first, err)
	}
	second, err := l.Allow(t.Context(), phone)
	if err == nil || second.Allowed || second.Tier != ratelimit.TierDevice || l.Fallbacks() != 2 {
		t.Fatalf("second decision %+v err %v fallbacks %d", second, err, l.Fallbacks())
	}
}

func TestWithoutSharedRedisEveryNodeCountsAlone(t *testing.T) {
	l, err := ratelimit.New(nil, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.UnixMilli(1_790_000_000_000)
	l.SetClock(func() time.Time { return clock })
	phone := ratelimit.Key{Tenant: "acme", User: "an", IP: "10.0.0.1", Device: "s1"}
	if d := allow(t, l, phone); !d.Allowed {
		t.Fatalf("first %+v", d)
	}
	if d := allow(t, l, phone); d.Allowed {
		t.Fatalf("second %+v", d)
	}
	clock = clock.Add(5 * time.Second)
	if d := allow(t, l, phone); !d.Allowed || l.Fallbacks() != 0 {
		t.Fatalf("after the window %+v fallbacks %d", d, l.Fallbacks())
	}
}

func TestLocalCountsStayBounded(t *testing.T) {
	c := ownerLimits()
	c.LocalUsers = 2
	l, err := ratelimit.New(nil, c)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		k := ratelimit.Key{Tenant: "acme", User: "u" + string(rune('a'+i%26)) + string(rune('a'+i/26)), IP: "10.0.0.1", Device: "s"}
		if d := allow(t, l, k); !d.Allowed {
			t.Fatalf("user %d refused %+v", i, d)
		}
	}
	if n := ratelimit.LocalSize(l); n > 6 {
		t.Fatalf("local counts grew to %d", n)
	}
}

func TestNewRefusesZeroLimits(t *testing.T) {
	c := ownerLimits()
	c.Device.Count = 0
	if _, err := ratelimit.New(redis.NewClient(&redis.Options{}), c); err == nil {
		t.Fatal("zero limit accepted")
	}
	c = ownerLimits()
	c.LocalUsers = 0
	if _, err := ratelimit.New(nil, c); err == nil {
		t.Fatal("zero local capacity accepted")
	}
}

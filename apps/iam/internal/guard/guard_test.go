package guard_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/iam/internal/guard"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var now = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

func TestBackoffDelays(t *testing.T) {
	b := guard.Backoff{FreeFailures: 5, MaxDelay: time.Minute}
	cases := map[int]time.Duration{
		0:       0,
		5:       0,
		6:       time.Second,
		7:       2 * time.Second,
		8:       4 * time.Second,
		11:      32 * time.Second,
		12:      time.Minute,
		1000:    time.Minute,
		1 << 40: time.Minute,
	}
	for failed, want := range cases {
		got := b.NextLoginAt(failed, now)
		if want == 0 {
			if !got.IsZero() {
				t.Errorf("NextLoginAt(%d) = %v, want no wait", failed, got)
			}
			continue
		}
		if !got.Equal(now.Add(want)) {
			t.Errorf("NextLoginAt(%d) = %v, want now + %v", failed, got.Sub(now), want)
		}
	}
}

func TestIPLimiterAllowsBurstThenRefills(t *testing.T) {
	l := guard.NewIPLimiter(1, 3, 10)
	for i := range 3 {
		if !l.Allow("10.0.0.1", now) {
			t.Fatalf("request %d inside the burst was refused", i+1)
		}
	}
	if l.Allow("10.0.0.1", now) {
		t.Fatal("request past the burst was allowed")
	}
	if !l.Allow("10.0.0.2", now) {
		t.Fatal("another ip was refused by the first ip's bucket")
	}
	if !l.Allow("10.0.0.1", now.Add(time.Second)) {
		t.Fatal("bucket did not refill after one second")
	}
}

func TestIPLimiterForgetsTheLeastRecentIP(t *testing.T) {
	l := guard.NewIPLimiter(1, 1, 2)
	l.Allow("a", now)
	l.Allow("b", now)
	l.Allow("c", now)
	if !l.Allow("a", now) {
		t.Fatal("evicted ip did not get a fresh bucket")
	}
	if l.Allow("c", now) {
		t.Fatal("recent ip lost its bucket")
	}
}

func TestHashSlotsCapConcurrentHashing(t *testing.T) {
	s := guard.NewHashSlots(2)
	release1, ok1 := s.TryAcquire()
	release2, ok2 := s.TryAcquire()
	if !ok1 || !ok2 || s.InUse() != 2 {
		t.Fatalf("two acquisitions = %v, %v, in use %d; want both and 2", ok1, ok2, s.InUse())
	}
	if _, ok := s.TryAcquire(); ok {
		t.Fatal("third acquisition succeeded with two slots")
	}
	release1()
	release1()
	if s.InUse() != 1 {
		t.Fatalf("in use after one release = %d, want 1 (double release must not free twice)", s.InUse())
	}
	release3, ok := s.TryAcquire()
	if !ok {
		t.Fatal("slot not reusable after release")
	}
	release2()
	release3()
	if s.InUse() != 0 {
		t.Fatalf("in use = %d, want 0", s.InUse())
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		header string
		value  string
		remote string
		want   string
	}{
		{"remote addr when no header configured", "", "", "192.0.2.1:5555", "192.0.2.1"},
		{"configured single header", "X-Real-IP", "198.51.100.7", "10.0.0.1:1", "198.51.100.7"},
		{"forwarded list uses the last hop", "X-Forwarded-For", "203.0.113.9, 198.51.100.7", "10.0.0.1:1", "198.51.100.7"},
		{"garbage falls back to remote addr", "X-Real-IP", "not-an-ip", "10.0.0.1:1", "10.0.0.1"},
		{"missing header falls back", "X-Real-IP", "", "10.0.0.1:1", "10.0.0.1"},
		{"ipv6 remote", "", "", "[2001:db8::1]:443", "2001:db8::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.RemoteAddr = c.remote
			if c.value != "" {
				r.Header.Set(c.header, c.value)
			}
			if got := guard.ClientIP(r, c.header); got != c.want {
				t.Fatalf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

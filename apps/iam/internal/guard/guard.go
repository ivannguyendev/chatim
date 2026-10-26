package guard

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/ivannguyendev/chatim/pkg/lru"
)

const maxBackoffShift = 30

type Backoff struct {
	FreeFailures int
	MaxDelay     time.Duration
}

func (b Backoff) NextLoginAt(failed int, now time.Time) time.Time {
	over := failed - b.FreeFailures
	if over <= 0 {
		return time.Time{}
	}
	delay := b.MaxDelay
	if shift := over - 1; shift < maxBackoffShift {
		if d := time.Second << shift; d < delay {
			delay = d
		}
	}
	return now.Add(delay)
}

type IPLimiter struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	buckets *lru.Cache[string, *rate.Limiter]
}

func NewIPLimiter(perSecond float64, burst, tracked int) *IPLimiter {
	return &IPLimiter{limit: rate.Limit(perSecond), burst: burst, buckets: lru.New[string, *rate.Limiter](tracked)}
}

func (l *IPLimiter) Allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket, ok := l.buckets.Get(ip)
	if !ok {
		bucket = rate.NewLimiter(l.limit, l.burst)
		l.buckets.Put(ip, bucket)
	}
	return bucket.AllowN(now, 1)
}

type HashSlots struct {
	slots chan struct{}
}

func NewHashSlots(n int) *HashSlots {
	return &HashSlots{slots: make(chan struct{}, n)}
}

func (s *HashSlots) TryAcquire() (func(), bool) {
	select {
	case s.slots <- struct{}{}:
		var released atomic.Bool
		return func() {
			if released.CompareAndSwap(false, true) {
				<-s.slots
			}
		}, true
	default:
		return nil, false
	}
}

func (s *HashSlots) InUse() int { return len(s.slots) }

func ClientIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			if i := strings.LastIndexByte(v, ','); i >= 0 {
				v = v[i+1:]
			}
			if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

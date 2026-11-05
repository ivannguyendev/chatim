package guard

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/ivannguyendev/chatim/pkg/core/lru"
)

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

func ClientIP(r *http.Request, header string) string {
	return ClientIPFrom(r.Header, r.RemoteAddr, header)
}

func ClientIPFrom(h http.Header, remoteAddr, header string) string {
	if header != "" {
		if v := h.Get(header); v != "" {
			if i := strings.LastIndexByte(v, ','); i >= 0 {
				v = v[i+1:]
			}
			if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

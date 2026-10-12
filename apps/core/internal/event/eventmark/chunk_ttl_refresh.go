package eventmark

import (
	"sync"
	"time"
)

const refreshLogCap = 1 << 16

type refreshLog struct {
	mu    sync.Mutex
	every time.Duration
	at    map[string]time.Time
}

func newRefreshLog(ttl time.Duration) *refreshLog {
	return &refreshLog{every: ttl / 4, at: make(map[string]time.Time)}
}

func (r *refreshLog) stale(chunks []string, now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	seen := make(map[string]bool, len(chunks))
	for _, c := range chunks {
		if seen[c] {
			continue
		}
		seen[c] = true
		if last, ok := r.at[c]; !ok || now.Sub(last) > r.every {
			out = append(out, c)
		}
	}
	return out
}

func (r *refreshLog) record(chunks []string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.at)+len(chunks) > refreshLogCap {
		clear(r.at)
	}
	for _, c := range chunks {
		r.at[c] = now
	}
}

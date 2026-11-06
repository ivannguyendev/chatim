package ratelimit

import "time"

func (l *Limiter) SetClock(now func() time.Time) { l.now = now }

func LocalSize(l *Limiter) int {
	l.local.mu.Lock()
	defer l.local.mu.Unlock()
	return len(l.local.counts)
}

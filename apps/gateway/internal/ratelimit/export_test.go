package ratelimit

import "time"

func (l *Limiter) SetClock(now func() time.Time) { l.now = now }

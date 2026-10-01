package dedupe

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type transition int

const (
	steady transition = iota
	entered
	recovered
)

type health struct {
	cooldown time.Duration
	log      *slog.Logger

	mu       sync.Mutex
	degraded bool
	epoch    uint64
	retryAt  time.Time
}

func (h *health) admit(now time.Time) (uint64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.degraded {
		if now.Before(h.retryAt) {
			return 0, false
		}
		h.retryAt = now.Add(h.cooldown)
	}
	return h.epoch, true
}

func (h *health) observe(ctx context.Context, epoch uint64, op string, err error, now time.Time) {
	switch h.record(epoch, err != nil, now) {
	case entered:
		h.log.WarnContext(ctx, "cid dedupe degraded to the local cache", "op", op, "err", err, "cooldown", h.cooldown)
	case recovered:
		h.log.InfoContext(ctx, "cid dedupe recovered", "op", op)
	default:
	}
}

func (h *health) record(epoch uint64, failed bool, now time.Time) transition {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case failed && !h.degraded:
		h.degraded, h.epoch, h.retryAt = true, h.epoch+1, now.Add(h.cooldown)
		return entered
	case failed:
		h.retryAt = now.Add(h.cooldown)
		return steady
	case h.degraded && epoch == h.epoch:
		h.degraded, h.epoch = false, h.epoch+1
		return recovered
	default:
		return steady
	}
}

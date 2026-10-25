package mongoindex

import (
	"context"
	"log/slog"
	"sync"
)

const OutdatedWarning = "index outdated, run /app indexes"

type Health struct {
	log   *slog.Logger
	mu    sync.Mutex
	stale map[string]bool
}

func NewHealth(log *slog.Logger) *Health {
	return &Health{log: log, stale: map[string]bool{}}
}

func (h *Health) Report(ctx context.Context, r Report) {
	h.mu.Lock()
	h.stale[r.Collection] = r.Stale()
	h.mu.Unlock()
	if r.Stale() {
		h.log.WarnContext(ctx, OutdatedWarning, "collection", r.Collection, "missing", r.MissingNames(), "outdated", r.OutdatedNames(), "conflicts", r.ConflictNames())
	}
}

func (h *Health) Outdated(collection string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stale[collection]
}

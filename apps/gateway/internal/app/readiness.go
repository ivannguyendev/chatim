package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ivannguyendev/chatim/pkg/core/admin"
)

type readiness struct {
	admin    *admin.Server
	log      *slog.Logger
	mu       sync.Mutex
	ready    bool
	stopping bool
}

func (r *readiness) identityBooted(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.ready {
		return
	}
	r.ready = true
	r.admin.SetReady(true)
	r.log.InfoContext(ctx, "gateway ready")
}

func (r *readiness) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopping = true
	r.admin.SetReady(false)
}

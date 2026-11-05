package app

import (
	"context"
	"log/slog"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/ivannguyendev/chatim/pkg/core/admin"
)

type readiness struct {
	admin    *admin.Server
	log      *slog.Logger
	mu       sync.Mutex
	chat     bool
	booted   bool
	ready    bool
	stopping bool
}

func (r *readiness) watch(ctx context.Context, conn *grpc.ClientConn) {
	conn.Connect()
	for {
		state := conn.GetState()
		r.chatReady(ctx, state == connectivity.Ready)
		if !conn.WaitForStateChange(ctx, state) {
			return
		}
	}
}

func (r *readiness) chatReady(ctx context.Context, ready bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chat = ready
	r.update(ctx)
}

func (r *readiness) identityBooted(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.booted = true
	r.update(ctx)
}

func (r *readiness) update(ctx context.Context) {
	ready := r.chat && r.booted
	if r.stopping || ready == r.ready {
		return
	}
	r.ready = ready
	r.admin.SetReady(ready)
	if ready {
		r.log.InfoContext(ctx, "gateway ready")
	} else {
		r.log.WarnContext(ctx, "gateway not ready: no chat connection is ready")
	}
}

func (r *readiness) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopping = true
	r.admin.SetReady(false)
}

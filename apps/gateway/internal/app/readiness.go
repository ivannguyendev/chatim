package app

import (
	"context"
	"log/slog"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/ivannguyendev/chatim/pkg/admin"
)

type readiness struct {
	admin    *admin.Server
	log      *slog.Logger
	mu       sync.Mutex
	core     bool
	booted   bool
	ready    bool
	stopping bool
}

func (r *readiness) watch(ctx context.Context, conn *grpc.ClientConn) {
	conn.Connect()
	for {
		state := conn.GetState()
		r.coreReady(ctx, state == connectivity.Ready)
		if !conn.WaitForStateChange(ctx, state) {
			return
		}
	}
}

func (r *readiness) coreReady(ctx context.Context, ready bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.core = ready
	r.update(ctx)
}

func (r *readiness) iamBooted(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.booted = true
	r.update(ctx)
}

func (r *readiness) update(ctx context.Context) {
	ready := r.core && r.booted
	if r.stopping || ready == r.ready {
		return
	}
	r.ready = ready
	r.admin.SetReady(ready)
	if ready {
		r.log.InfoContext(ctx, "gateway ready")
	} else {
		r.log.WarnContext(ctx, "gateway not ready: no core connection is ready")
	}
}

func (r *readiness) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopping = true
	r.admin.SetReady(false)
}

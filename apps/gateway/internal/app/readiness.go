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
	if r.stopping || ready == r.core {
		return
	}
	r.core = ready
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

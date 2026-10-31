package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/pkg/admin"
)

const storeCallTimeout = 3 * time.Second

type directoryReadiness interface {
	Ready() bool
}

type health struct {
	admin     *admin.Server
	log       *slog.Logger
	store     Store
	ring      *keyring.Ring
	directory directoryReadiness
	schema    int
	wake      chan struct{}
	mu        sync.Mutex
	checked   bool
	ready     bool
	wasReady  bool
	stopping  bool
}

func newHealth(srv *admin.Server, log *slog.Logger, st Store, ring *keyring.Ring, dir directoryReadiness, schema int) *health {
	return &health{admin: srv, log: log, store: st, ring: ring, directory: dir, schema: schema, wake: make(chan struct{}, 1)}
}

func (h *health) trigger() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *health) check(ctx context.Context) {
	h.mu.Lock()
	probeStore := !h.wasReady
	h.mu.Unlock()
	checkCtx, cancel := context.WithTimeout(ctx, storeCallTimeout)
	defer cancel()
	reloadErr := h.ring.Reload(checkCtx)
	var pingErr, versionErr error
	version := h.schema
	if probeStore {
		pingErr = h.store.Ping(checkCtx)
		version, versionErr = h.store.SchemaVersion(checkCtx)
	}
	storeOK := pingErr == nil && versionErr == nil && version == h.schema
	keyReady, directoryReady := h.ring.Ready(), h.directory.Ready()
	h.mu.Lock()
	defer h.mu.Unlock()
	ready := keyReady && directoryReady && storeOK
	if h.stopping || (h.checked && ready == h.ready) {
		return
	}
	h.checked = true
	h.ready = ready
	h.wasReady = h.wasReady || ready
	h.admin.SetReady(ready)
	if ready {
		h.log.InfoContext(ctx, "iam ready")
		return
	}
	h.log.WarnContext(ctx, "iam not ready",
		"signing_key", keyReady, "directory", directoryReady, "reload_err", errText(reloadErr), "store_err", errText(pingErr),
		"schema_version", version, "schema_expected", h.schema, "schema_err", errText(versionErr))
}

func (h *health) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopping = true
	h.ready = false
	h.admin.SetReady(false)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

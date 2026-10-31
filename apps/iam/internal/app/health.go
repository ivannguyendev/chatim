package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/pkg/admin"
)

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
	mu        sync.Mutex
	running   bool
	again     bool
	checked   bool
	ready     bool
	wasReady  bool
	stopping  bool
}

func (h *health) check(ctx context.Context) {
	h.mu.Lock()
	if h.running {
		h.again = true
		h.mu.Unlock()
		return
	}
	h.running = true
	h.mu.Unlock()
	for {
		h.evaluate(ctx)
		h.mu.Lock()
		if !h.again || ctx.Err() != nil {
			h.running, h.again = false, false
			h.mu.Unlock()
			return
		}
		h.again = false
		h.mu.Unlock()
	}
}

func (h *health) evaluate(ctx context.Context) {
	reloadErr := h.ring.Reload(ctx)
	pingErr := h.store.Ping(ctx)
	version, versionErr := h.store.SchemaVersion(ctx)
	storeOK := pingErr == nil && versionErr == nil && version == h.schema
	keyReady, directoryReady := h.ring.Ready(), h.directory.Ready()
	h.mu.Lock()
	defer h.mu.Unlock()
	ready := keyReady && directoryReady && (storeOK || h.wasReady)
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

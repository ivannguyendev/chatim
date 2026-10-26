package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/pkg/admin"
)

type health struct {
	admin    *admin.Server
	log      *slog.Logger
	store    Store
	ring     *keyring.Ring
	schema   int
	mu       sync.Mutex
	checked  bool
	ready    bool
	stopping bool
}

func (h *health) check(ctx context.Context) {
	reloadErr := h.ring.Reload(ctx)
	pingErr := h.store.Ping(ctx)
	version, versionErr := h.store.SchemaVersion(ctx)
	ready := h.ring.Ready() && pingErr == nil && versionErr == nil && version == h.schema
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopping || (h.checked && ready == h.ready) {
		return
	}
	h.checked = true
	h.ready = ready
	h.admin.SetReady(ready)
	if ready {
		h.log.InfoContext(ctx, "iam ready")
		return
	}
	h.log.WarnContext(ctx, "iam not ready",
		"signing_key", h.ring.Ready(), "reload_err", errText(reloadErr), "store_err", errText(pingErr),
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

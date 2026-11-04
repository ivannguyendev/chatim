package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/keyring"
	"github.com/ivannguyendev/chatim/pkg/core/admin"
)

const storeCallTimeout = 3 * time.Second

type readiness interface {
	Ready() bool
}

type health struct {
	admin      *admin.Server
	log        *slog.Logger
	store      Store
	ring       *keyring.Ring
	directory  readiness
	trust      readiness
	schema     *schemaStats
	wake       chan struct{}
	schemaDone chan struct{}
	schemaOnce sync.Once
	mu         sync.Mutex
	checked    bool
	ready      bool
	wasReady   bool
	stopping   bool
}

type storeProbe struct {
	pingErr   error
	schemaErr error
	missing   int
	complete  bool
}

func newHealth(srv *admin.Server, log *slog.Logger, st Store, ring *keyring.Ring, dir, trusted readiness, schema *schemaStats) *health {
	return &health{
		admin: srv, log: log, store: st, ring: ring, directory: dir, trust: trusted, schema: schema,
		wake: make(chan struct{}, 1), schemaDone: make(chan struct{}),
	}
}

func (h *health) trigger() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *health) schemaComplete() bool {
	select {
	case <-h.schemaDone:
		return true
	default:
		return false
	}
}

func (h *health) check(ctx context.Context) {
	h.mu.Lock()
	probeStore := !h.wasReady
	h.mu.Unlock()
	checkCtx, cancel := context.WithTimeout(ctx, storeCallTimeout)
	defer cancel()
	reloadErr := h.ring.Reload(checkCtx)
	probe := storeProbe{complete: true}
	if probeStore {
		probe = h.probeStore(checkCtx)
	}
	keyReady, directoryReady, trustReady := h.ring.Ready(), h.directory.Ready(), h.trust.Ready()
	h.mu.Lock()
	defer h.mu.Unlock()
	ready := keyReady && directoryReady && trustReady && probe.complete
	if h.stopping || (h.checked && ready == h.ready) {
		return
	}
	h.checked = true
	h.ready = ready
	h.wasReady = h.wasReady || ready
	h.admin.SetReady(ready)
	if ready {
		h.log.InfoContext(ctx, "identity ready")
		return
	}
	h.log.WarnContext(ctx, "identity not ready",
		"signing_key", keyReady, "directory", directoryReady, "trust", trustReady, "reload_err", errText(reloadErr), "store_err", errText(probe.pingErr),
		"schema_missing", probe.missing, "schema_err", errText(probe.schemaErr))
}

func (h *health) probeStore(ctx context.Context) storeProbe {
	var p storeProbe
	if p.pingErr = h.store.Ping(ctx); p.pingErr != nil {
		return p
	}
	state, err := h.store.Schema(ctx)
	if err != nil {
		p.schemaErr = err
		return p
	}
	h.schema.note(ctx, h.log, state)
	p.missing = len(state.Missing)
	p.complete = p.missing == 0
	if p.complete {
		h.schemaOnce.Do(func() { close(h.schemaDone) })
	}
	return p
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

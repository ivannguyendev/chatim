package slot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

type Config struct {
	CoreID       string
	Addr         string
	Tick         time.Duration
	HeartbeatTTL time.Duration
	LeaseTTL     time.Duration

	BeforeRelease func(ctx context.Context, slot uint16)
}

type Manager struct {
	cfg   Config
	rdb   redis.UniversalClient
	log   *slog.Logger
	mu    sync.RWMutex
	owned map[uint16]time.Time
	now   func() time.Time
}

func New(rdb redis.UniversalClient, cfg Config, log *slog.Logger) (*Manager, error) {
	if cfg.CoreID == "" || strings.ContainsAny(cfg.CoreID, "*?[]\\ ") {
		return nil, errors.New("slot: CoreID must be non-empty and free of glob characters and spaces")
	}
	cfg.Tick = cmp.Or(cfg.Tick, time.Second)
	cfg.HeartbeatTTL = cmp.Or(cfg.HeartbeatTTL, 5*time.Second)
	cfg.LeaseTTL = cmp.Or(cfg.LeaseTTL, 10*time.Second)
	if cfg.LeaseTTL <= 2*cfg.Tick || cfg.HeartbeatTTL <= 2*cfg.Tick {
		return nil, errors.New("slot: LeaseTTL and HeartbeatTTL must each exceed 2×Tick")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{cfg: cfg, rdb: rdb, log: log.With("core", cfg.CoreID), owned: map[uint16]time.Time{}, now: time.Now}, nil
}

func (m *Manager) Owns(slot uint16) bool {
	m.mu.RLock()
	at, ok := m.owned[slot]
	m.mu.RUnlock()
	return ok && m.now().Sub(at) < min(m.cfg.LeaseTTL, m.cfg.HeartbeatTTL)-m.cfg.Tick
}

func (m *Manager) Owned() []uint16 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]uint16, 0, len(m.owned))
	for s := range m.owned {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func (m *Manager) Run(ctx context.Context) error {
	t := time.NewTicker(m.cfg.Tick)
	defer t.Stop()
	for {
		if err := m.Step(ctx); err != nil && ctx.Err() == nil {
			m.log.Warn("slot reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return m.ReleaseAll(rctx)
		case <-t.C:
		}
	}
}

func (m *Manager) Step(ctx context.Context) error {
	stamp := m.now()
	if err := m.rdb.Set(ctx, slotmap.CoreKey(m.cfg.CoreID), m.cfg.Addr, m.cfg.HeartbeatTTL).Err(); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	alive, err := m.aliveCores(ctx)
	if err != nil {
		return err
	}
	lost, err := m.renew(ctx, stamp)
	if err != nil {
		return err
	}
	target := (slotmap.Count + len(alive) - 1) / len(alive)
	moved := false
	switch n := len(m.Owned()); {
	case n > target:
		moved, err = true, m.release(ctx, n-target)
	case n < target:
		moved, err = m.claim(ctx, alive, target-n, stamp)
	}
	if lost || moved {
		err = errors.Join(err, m.rdb.Publish(ctx, slotmap.ChangedChannel, m.cfg.CoreID).Err())
	}
	return err
}

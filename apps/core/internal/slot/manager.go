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

const ReleaseTimeout = 5 * time.Second

type Config struct {
	CoreID       string
	Addr         string
	Tick         time.Duration
	HeartbeatTTL time.Duration
	LeaseTTL     time.Duration
	HookTimeout  time.Duration

	BeforeRelease func(ctx context.Context, slots []uint16)
	AfterClaim    func(ctx context.Context, slots []uint16)
	AfterLose     func(ctx context.Context, slots []uint16)
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
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{cfg: cfg, rdb: rdb, log: log.With("core", cfg.CoreID), owned: map[uint16]time.Time{}, now: time.Now}, nil
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Tick = cmp.Or(c.Tick, time.Second)
	c.HeartbeatTTL = cmp.Or(c.HeartbeatTTL, 5*time.Second)
	c.LeaseTTL = cmp.Or(c.LeaseTTL, 10*time.Second)
	c.HookTimeout = cmp.Or(c.HookTimeout, c.Tick/2)
	return c
}

func (c Config) validate() error {
	switch {
	case c.CoreID == "" || strings.ContainsAny(c.CoreID, "*?[]\\ "):
		return errors.New("slot: CoreID must be non-empty and free of glob characters and spaces")
	case c.Tick <= 0:
		return errors.New("slot: Tick must be positive")
	case c.LeaseTTL <= 2*c.Tick || c.HeartbeatTTL <= 2*c.Tick:
		return errors.New("slot: LeaseTTL and HeartbeatTTL must each exceed 2×Tick")
	case c.HookTimeout <= 0 || c.HookTimeout >= c.Tick:
		return errors.New("slot: HookTimeout must be positive and shorter than Tick")
	default:
		return nil
	}
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
			m.log.WarnContext(ctx, "slot reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			rctx, cancel := context.WithTimeout(context.Background(), ReleaseTimeout)
			defer cancel()
			return m.ReleaseAll(rctx)
		case <-t.C:
		}
	}
}

func (m *Manager) Step(ctx context.Context) error {
	stamp := m.now()
	hookCtx, cancel := context.WithTimeout(ctx, m.cfg.HookTimeout)
	defer cancel()
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
	deliver(hookCtx, m.cfg.AfterLose, lost)
	target := (slotmap.Count + len(alive) - 1) / len(alive)
	moved := false
	switch n := len(m.Owned()); {
	case n > target:
		moved, err = true, m.release(ctx, hookCtx, n-target)
	case n < target:
		var claimed []uint16
		claimed, err = m.claim(ctx, alive, target-n, stamp)
		moved = len(claimed) > 0
		deliver(hookCtx, m.cfg.AfterClaim, claimed)
	}
	if len(lost) > 0 || moved {
		err = errors.Join(err, m.rdb.Publish(ctx, slotmap.ChangedChannel, m.cfg.CoreID).Err())
	}
	return err
}

func deliver(ctx context.Context, hook func(context.Context, []uint16), slots []uint16) {
	if hook != nil && len(slots) > 0 {
		hook(ctx, slots)
	}
}

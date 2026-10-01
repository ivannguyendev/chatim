package slot

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const renewScript = `
local res = {}
for i, k in ipairs(KEYS) do
  if redis.call('GET', k) == ARGV[1] then
    redis.call('PEXPIRE', k, ARGV[2])
    res[i] = 1
  else
    res[i] = 0
  end
end
return res`

const claimScript = `
local cur = redis.call('GET', KEYS[1])
if (cur == false and ARGV[1] == '') or cur == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
  return 1
end
return 0`

const releaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

func (m *Manager) aliveCores(ctx context.Context) (map[string]bool, error) {
	alive := map[string]bool{m.cfg.CoreID: true}
	iter := m.rdb.Scan(ctx, 0, slotmap.CorePattern, 256).Iterator()
	for iter.Next(ctx) {
		if id, ok := slotmap.CoreIDFromKey(iter.Val()); ok {
			alive[id] = true
		}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan cores: %w", err)
	}
	return alive, nil
}

func (m *Manager) renew(ctx context.Context, stamp time.Time) (lost []uint16, err error) {
	slots := m.Owned()
	if len(slots) == 0 {
		return nil, nil
	}
	keys := make([]string, len(slots))
	for i, s := range slots {
		keys[i] = slotmap.SlotKey(s)
	}
	kept, err := m.rdb.Eval(ctx, renewScript, keys, m.cfg.CoreID, m.cfg.LeaseTTL.Milliseconds()).Int64Slice()
	if err != nil {
		return nil, fmt.Errorf("renew: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range slots {
		if kept[i] == 1 {
			m.owned[s] = stamp
			continue
		}
		delete(m.owned, s)
		lost = append(lost, s)
	}
	return lost, nil
}

func (m *Manager) claim(ctx context.Context, alive map[string]bool, need int, stamp time.Time) ([]uint16, error) {
	keys := make([]string, slotmap.Count)
	for s := range keys {
		keys[s] = slotmap.SlotKey(uint16(s))
	}
	owners, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read owners: %w", err)
	}
	type candidate struct {
		slot   uint16
		expect string
		score  uint64
	}
	var cands []candidate
	for s, v := range owners {
		owner, _ := v.(string)
		if m.Owns(uint16(s)) || (owner != "" && owner != m.cfg.CoreID && alive[owner]) {
			continue
		}
		cands = append(cands, candidate{uint16(s), owner, slotmap.Score(uint16(s), m.cfg.CoreID)})
	}
	slices.SortFunc(cands, func(a, b candidate) int { return cmp.Compare(b.score, a.score) })
	cands = cands[:min(need, len(cands))]
	if len(cands) == 0 {
		return nil, nil
	}
	pipe := m.rdb.Pipeline()
	cmds := make([]*redis.Cmd, len(cands))
	for i, c := range cands {
		cmds[i] = pipe.Eval(ctx, claimScript, []string{keys[c.slot]}, c.expect, m.cfg.CoreID, m.cfg.LeaseTTL.Milliseconds())
	}
	_, execErr := pipe.Exec(ctx)
	var claimed []uint16
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range cands {
		if n, err := cmds[i].Int64(); err == nil && n == 1 {
			m.owned[c.slot] = stamp
			claimed = append(claimed, c.slot)
		}
	}
	if execErr != nil {
		return claimed, fmt.Errorf("claim: %w", execErr)
	}
	return claimed, nil
}

func (m *Manager) release(ctx, hookCtx context.Context, count int) error {
	slots := m.Owned()
	slices.SortFunc(slots, func(a, b uint16) int {
		return cmp.Compare(slotmap.Score(a, m.cfg.CoreID), slotmap.Score(b, m.cfg.CoreID))
	})
	return m.releaseSlots(ctx, hookCtx, slots[:count:count])
}

func (m *Manager) ReleaseAll(ctx context.Context) error {
	if slots := m.Owned(); len(slots) > 0 {
		hookCtx, cancel := context.WithTimeout(ctx, m.cfg.HookTimeout)
		err := m.releaseSlots(ctx, hookCtx, slots)
		cancel()
		if err != nil {
			return err
		}
	}
	if err := m.rdb.Del(ctx, slotmap.CoreKey(m.cfg.CoreID)).Err(); err != nil {
		return fmt.Errorf("remove heartbeat: %w", err)
	}
	return m.rdb.Publish(ctx, slotmap.ChangedChannel, m.cfg.CoreID).Err()
}

func (m *Manager) releaseSlots(ctx, hookCtx context.Context, slots []uint16) error {
	m.mu.Lock()
	for _, s := range slots {
		delete(m.owned, s)
	}
	m.mu.Unlock()
	deliver(hookCtx, m.cfg.BeforeRelease, slots)
	pipe := m.rdb.Pipeline()
	for _, s := range slots {
		pipe.Eval(ctx, releaseScript, []string{slotmap.SlotKey(s)}, m.cfg.CoreID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

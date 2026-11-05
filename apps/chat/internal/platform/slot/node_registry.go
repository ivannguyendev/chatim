package slot

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const heartbeatScript = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
redis.call('ZADD', KEYS[2], string.format('%.0f', now + tonumber(ARGV[3])), ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', string.format('%.0f', now))
return redis.call('ZRANGEBYSCORE', KEYS[2], '(' .. string.format('%.0f', now), '+inf', 'LIMIT', 0, ARGV[4])`

func (m *Manager) heartbeat(ctx context.Context) (map[string]bool, error) {
	keys := []string{slotmap.NodeKey(m.cfg.NodeID), slotmap.NodeRegistryKey}
	ids, err := m.rdb.Eval(ctx, heartbeatScript, keys, m.cfg.NodeID, m.cfg.Addr, m.cfg.HeartbeatTTL.Milliseconds(), slotmap.MaxNodes).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("heartbeat: %w", err)
	}
	alive := make(map[string]bool, len(ids)+1)
	alive[m.cfg.NodeID] = true
	for _, id := range ids {
		alive[id] = true
	}
	return alive, nil
}

func (m *Manager) deregister(ctx context.Context) error {
	pipe := m.rdb.TxPipeline()
	pipe.Del(ctx, slotmap.NodeKey(m.cfg.NodeID))
	pipe.ZRem(ctx, slotmap.NodeRegistryKey, m.cfg.NodeID)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("remove heartbeat: %w", err)
	}
	return nil
}

package slotmap

import (
	"context"
	"fmt"
	"slices"
)

type node struct{ id, addr string }

type table struct {
	nodes []node
	route [Count]*node
	owned [Count]bool
}

func (r *Resolver) load(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.LoadTimeout)
	defer cancel()
	nodes, err := r.liveNodes(ctx)
	if err != nil {
		return err
	}
	owners, err := r.rdb.MGet(ctx, r.slotKeys...).Result()
	if err != nil {
		return fmt.Errorf("read slot owners: %w", err)
	}
	t := buildTable(nodes, owners)
	r.table.Store(t)
	r.once.Do(func() { close(r.ready) })
	if r.cfg.OnLoad != nil {
		r.cfg.OnLoad(t.addrs())
	}
	return nil
}

func (t *table) addrs() []string {
	addrs := make([]string, len(t.nodes))
	for i, c := range t.nodes {
		addrs[i] = c.addr
	}
	return addrs
}

const liveNodesScript = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
return redis.call('ZRANGEBYSCORE', KEYS[1], '(' .. string.format('%.0f', now), '+inf', 'LIMIT', 0, ARGV[1])`

func (r *Resolver) liveNodes(ctx context.Context) ([]node, error) {
	ids, err := r.rdb.Eval(ctx, liveNodesScript, []string{NodeRegistryKey}, MaxNodes).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("read node registry: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	slices.Sort(ids)
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = NodeKey(id)
	}
	addrs, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read node addresses: %w", err)
	}
	nodes := make([]node, 0, len(ids))
	for i, id := range ids {
		if addr, _ := addrs[i].(string); id != "" && addr != "" {
			nodes = append(nodes, node{id: id, addr: addr})
		}
	}
	return nodes, nil
}

func buildTable(nodes []node, owners []any) *table {
	t := &table{nodes: nodes}
	byID := make(map[string]*node, len(nodes))
	ids := make([]string, len(nodes))
	for i := range nodes {
		byID[nodes[i].id] = &nodes[i]
		ids[i] = nodes[i].id
	}
	for s := range uint16(Count) {
		owner, _ := owners[s].(string)
		if c, ok := byID[owner]; ok {
			t.route[s], t.owned[s] = c, true
			continue
		}
		t.route[s] = byID[Preferred(s, ids)]
	}
	return t
}

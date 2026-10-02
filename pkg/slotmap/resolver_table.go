package slotmap

import (
	"context"
	"fmt"
	"slices"
)

type core struct{ id, addr string }

type table struct {
	cores []core
	route [Count]*core
	owned [Count]bool
}

func (r *Resolver) load(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.LoadTimeout)
	defer cancel()
	cores, err := r.liveCores(ctx)
	if err != nil {
		return err
	}
	owners, err := r.rdb.MGet(ctx, r.slotKeys...).Result()
	if err != nil {
		return fmt.Errorf("read slot owners: %w", err)
	}
	r.table.Store(buildTable(cores, owners))
	r.once.Do(func() { close(r.ready) })
	return nil
}

const liveCoresScript = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
return redis.call('ZRANGEBYSCORE', KEYS[1], '(' .. string.format('%.0f', now), '+inf', 'LIMIT', 0, ARGV[1])`

func (r *Resolver) liveCores(ctx context.Context) ([]core, error) {
	ids, err := r.rdb.Eval(ctx, liveCoresScript, []string{CoreRegistryKey}, MaxCores).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("read core registry: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	slices.Sort(ids)
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = CoreKey(id)
	}
	addrs, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read core addresses: %w", err)
	}
	cores := make([]core, 0, len(ids))
	for i, id := range ids {
		if addr, _ := addrs[i].(string); id != "" && addr != "" {
			cores = append(cores, core{id: id, addr: addr})
		}
	}
	return cores, nil
}

func buildTable(cores []core, owners []any) *table {
	t := &table{cores: cores}
	byID := make(map[string]*core, len(cores))
	ids := make([]string, len(cores))
	for i := range cores {
		byID[cores[i].id] = &cores[i]
		ids[i] = cores[i].id
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

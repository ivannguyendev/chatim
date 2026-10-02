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

func (r *Resolver) liveCores(ctx context.Context) ([]core, error) {
	seen := make(map[string]struct{}, 8)
	iter := r.rdb.Scan(ctx, 0, CorePattern, 256).Iterator()
	for len(seen) < MaxCores && iter.Next(ctx) {
		seen[iter.Val()] = struct{}{}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan cores: %w", err)
	}
	if len(seen) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	addrs, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("read core addresses: %w", err)
	}
	cores := make([]core, 0, len(keys))
	for i, k := range keys {
		id, _ := CoreIDFromKey(k)
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

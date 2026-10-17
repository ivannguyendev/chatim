package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func seed(ctx context.Context, cl *route.Client, probe *mongoProbe, c config, run string) (layout, error) {
	l := planLayout(c, run)
	specs := l.rooms()
	members := 0
	for _, s := range specs {
		members += len(s.members)
	}
	fmt.Printf("roomlistbench run=%s tenant=%s sizes=%v users-per-size=%d private=%d rooms=%d (background %d, private %d) member docs=%d workers=%d\n",
		run, c.tenant, c.sizes, c.perSize, c.private, len(specs), len(l.background), len(l.private), members, c.workers)
	started := time.Now()
	ids, err := seedRooms(ctx, cl, c, run, specs)
	if err != nil {
		return layout{}, err
	}
	fmt.Printf("seed: %d rooms created in %v\n", len(ids), time.Since(started).Round(time.Millisecond))
	if err := assignRooms(l, specs, ids); err != nil {
		return layout{}, err
	}
	bgIDs, privateIDs := ids[:len(l.background)], ids[len(l.background):]
	started = time.Now()
	bgSent, err := sendMessages(ctx, cl, c, run, "b", l.background, bgIDs)
	if err != nil {
		return layout{}, err
	}
	privateSent, err := sendMessages(ctx, cl, c, run, "p", l.private, privateIDs)
	if err != nil {
		return layout{}, err
	}
	fmt.Printf("seed: %d background and %d private messages sent in %v\n", bgSent, privateSent, time.Since(started).Round(time.Millisecond))
	started = time.Now()
	if c.backgroundMessages > 0 {
		if err := probe.awaitActivity(ctx, bgIDs, int64(c.backgroundMessages), c.settle); err != nil {
			return layout{}, err
		}
	}
	if err := probe.awaitActivity(ctx, privateIDs, int64(c.privateMessages), c.settle); err != nil {
		return layout{}, err
	}
	fmt.Printf("settle: room activity applied to every room after %v\n", time.Since(started).Round(time.Second))
	return l, nil
}

func measureAll(ctx context.Context, probe *mongoProbe, c config, users []*benchUser) error {
	api, closer, err := route.DialInsecure(c.core)
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	m := &meter{api: api, probe: probe, metrics: c.metrics, tenant: c.tenant, page: uint32(c.page), repeats: c.repeats, cells: map[string]*cell{}}
	for _, size := range c.sizes {
		started := time.Now()
		rate, err := probe.awaitQuiet(ctx, c.quietOps, c.settle)
		if err != nil {
			return err
		}
		base, err := m.baseline(ctx, 20)
		if err != nil {
			return err
		}
		bm, bx := medianMax(base)
		fmt.Printf("rooms=%d: mongo quiet (%d ops/s) after %v; idle 20ms bracket reads median=%d max=%d\n", size, rate, time.Since(started).Round(time.Second), bm, bx)
		measured := 0
		for _, u := range users {
			if u.size != size {
				continue
			}
			if err := m.measureUser(ctx, u); err != nil {
				return err
			}
			measured++
		}
		if measured == 0 {
			return fmt.Errorf("layout %s has no user with %d rooms", c.layoutPath, size)
		}
		m.report(size, c)
	}
	return nil
}

func (m *meter) report(size int, c config) {
	path := "join"
	if size > c.joinMax {
		path = "stepwise"
	}
	fmt.Printf("ListMyRooms on %s, limit %d; reads = mongo opcounters query+command per call, getmore separate; caches = room_cache_* deltas per call\n", c.core, c.page)
	for _, phase := range phases {
		cl := m.cells[cellKey(size, phase)]
		if cl == nil {
			continue
		}
		s := cl.lat.Summary()
		rm, rx := medianMax(cl.reads)
		rmin := slices.Min(cl.reads)
		gm, _ := medianMax(cl.getMores)
		cm, cx := medianMax(cl.colls)
		fmt.Printf("rooms=%d path=%s %-10s n=%d p50=%v p99=%v max=%v opcounter reads min=%d median=%d max=%d getmore median=%d new mongo conns %d; chatim.* reads median=%d max=%d per call: %s; caches: %v\n",
			size, path, phase, s.Count, s.P50.Round(10*time.Microsecond), s.P99.Round(10*time.Microsecond), s.Max.Round(10*time.Microsecond), rmin, rm, rx, gm, cl.conns, cm, cx, cl.byColl.per(s.Count), cl.caches.per(s.Count))
	}
}

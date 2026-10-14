package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

var phases = []string{"first cold", "next cold", "first warm", "next warm"}

type cell struct {
	lat      latency.Recorder
	reads    []int64
	getMores []int64
	colls    []int64
	conns    int64
	byColl   nsReads
	caches   cacheCounts
}

type meter struct {
	api     chatimv1.CoreServiceClient
	probe   *mongoProbe
	metrics string
	tenant  string
	page    uint32
	repeats int
	cells   map[string]*cell
}

func cellKey(size int, phase string) string { return fmt.Sprintf("%d %s", size, phase) }

func (m *meter) cell(key string) *cell {
	c, ok := m.cells[key]
	if !ok {
		c = &cell{caches: cacheCounts{}, byColl: nsReads{}}
		m.cells[key] = c
	}
	return c
}

func (m *meter) call(ctx context.Context, key, user, next string) (*chatimv1.ListMyRoomsResponse, error) {
	cachesBefore, err := scrapeCaches(ctx, m.metrics)
	if err != nil {
		return nil, err
	}
	topBefore, err := m.probe.top(ctx)
	if err != nil {
		return nil, err
	}
	before, err := m.probe.counters(ctx)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := m.api.ListMyRooms(route.WithCaller(ctx, m.tenant, user), &chatimv1.ListMyRoomsRequest{Folder: chatimv1.RoomFolder_ROOM_FOLDER_MAIN, Next: next, Limit: m.page})
	took := time.Since(started)
	if err != nil {
		return nil, fmt.Errorf("ListMyRooms %s for %s: %w", key, user, err)
	}
	after, err := m.probe.counters(ctx)
	if err != nil {
		return nil, err
	}
	topAfter, err := m.probe.top(ctx)
	if err != nil {
		return nil, err
	}
	cachesAfter, err := scrapeCaches(ctx, m.metrics)
	if err != nil {
		return nil, err
	}
	c := m.cell(key)
	c.lat.Add(took)
	d := after.minus(before)
	c.reads = append(c.reads, d.reads())
	c.getMores = append(c.getMores, d.GetMore)
	c.conns += d.Conns
	colls := topAfter.minus(topBefore)
	c.colls = append(c.colls, colls.total())
	c.byColl.add(colls)
	c.caches.add(cachesAfter.minus(cachesBefore))
	return resp, nil
}

func (m *meter) measureUser(ctx context.Context, u *benchUser) error {
	if err := m.pair(ctx, u, "first cold", "next cold"); err != nil {
		return err
	}
	for range m.repeats {
		if err := m.pair(ctx, u, "first warm", "next warm"); err != nil {
			return err
		}
	}
	return nil
}

func (m *meter) pair(ctx context.Context, u *benchUser, first, next string) error {
	cursor := ""
	for _, phase := range []string{first, next} {
		resp, err := m.call(ctx, cellKey(u.size, phase), u.name, cursor)
		if err != nil {
			return err
		}
		if err := checkPrivatePage(u, resp, int(m.page)); err != nil {
			return fmt.Errorf("%s %s: %w", u.name, phase, err)
		}
		cursor = resp.GetNext()
	}
	return nil
}

func checkPrivatePage(u *benchUser, resp *chatimv1.ListMyRoomsResponse, want int) error {
	items := resp.GetItems()
	if len(items) != want || resp.GetNext() == "" {
		return fmt.Errorf("page has %d items and next %q, want %d items and a next cursor", len(items), resp.GetNext(), want)
	}
	for _, it := range items {
		if !slices.Contains(u.rooms, it.GetRoom().GetId()) {
			return fmt.Errorf("room %s on the page is not one of the user's private rooms (activity not settled?)", it.GetRoom().GetId())
		}
		if it.GetPreview().GetSeq() == 0 || it.GetUnread().GetCount() == 0 {
			return fmt.Errorf("room %s has preview seq %d and unread %d, want both set", it.GetRoom().GetId(), it.GetPreview().GetSeq(), it.GetUnread().GetCount())
		}
	}
	return nil
}

func (m *meter) baseline(ctx context.Context, n int) ([]int64, error) {
	out := make([]int64, 0, n)
	for range n {
		before, err := m.probe.counters(ctx)
		if err != nil {
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
		after, err := m.probe.counters(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, after.minus(before).reads())
	}
	return out, nil
}

func medianMax(v []int64) (int64, int64) {
	if len(v) == 0 {
		return 0, 0
	}
	s := slices.Sorted(slices.Values(v))
	return s[(len(s)-1)/2], s[len(s)-1]
}

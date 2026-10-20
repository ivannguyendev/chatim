package roomlist

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type Query struct {
	Tenant string
	User   string
	Folder domain.RoomFolder
	After  *domain.RoomSortKey
	Limit  int
}

type Page struct {
	Items []domain.RoomItem
	Next  *domain.RoomSortKey
}

func (l *Lister) List(ctx context.Context, q Query) (Page, error) {
	switch {
	case q.Limit < 1 || q.Limit > MaxRooms:
		return Page{}, errLimit
	case q.Folder != domain.FolderMain && q.Folder != domain.FolderArchived:
		return Page{}, errFolder
	}
	key := roomcache.SnapshotKey{Tenant: q.Tenant, User: q.User, Folder: q.Folder}
	if q.After != nil {
		if keys, more, ok := l.snaps.Page(key, q.After, q.Limit); ok {
			return l.page(ctx, q, keys, more, slotsOfKeys(keys))
		}
	}
	rows, full, err := l.order(ctx, q)
	if err != nil {
		return Page{}, err
	}
	keys := make([]domain.RoomSortKey, len(rows))
	for i, r := range rows {
		keys[i] = r.SortKey()
	}
	l.snaps.Store(key, keys)
	start := 0
	if q.After != nil {
		start, _ = slices.BinarySearchFunc(keys, *q.After, domain.CompareRoomSortKeys)
		for start < len(keys) && domain.CompareRoomSortKeys(keys[start], *q.After) <= 0 {
			start++
		}
	}
	end := min(len(rows), start+q.Limit)
	return l.page(ctx, q, keys[start:end], end < len(rows), slotsOfRows(rows[start:end], full))
}

func (l *Lister) order(ctx context.Context, q Query) ([]store.RoomOrderRow, bool, error) {
	path, err := l.path(ctx, q.Tenant, q.User)
	if err != nil {
		return nil, false, err
	}
	sq := store.RoomListQuery{Tenant: q.Tenant, User: q.User, Folder: q.Folder}
	read, op := l.list.StepwiseRoomOrder, "read the stepwise room order"
	if path == roomcache.PathJoin {
		read, op = l.list.JoinedRoomOrder, "read the joined room order"
	}
	rows, err := read(ctx, sq)
	if err != nil {
		return nil, false, storeErr(op, err)
	}
	store.SortRoomOrder(rows)
	return rows, path == roomcache.PathJoin, nil
}

func (l *Lister) path(ctx context.Context, tenant, user string) (roomcache.ListPath, error) {
	if p, ok := l.paths.Get(tenant, user); ok {
		return p, nil
	}
	n, err := l.list.CountRooms(ctx, tenant, user, l.joinMax+1)
	if err != nil {
		return 0, storeErr("count rooms", err)
	}
	p := roomcache.ChoosePath(n, l.joinMax)
	l.paths.Put(tenant, user, p)
	return p, nil
}

func (l *Lister) page(ctx context.Context, q Query, keys []domain.RoomSortKey, more bool, slots []slot) (Page, error) {
	items, _, err := l.build(ctx, who{tenant: q.Tenant, user: q.User}, slots)
	if err != nil {
		return Page{}, err
	}
	p := Page{Items: slices.DeleteFunc(items, func(it domain.RoomItem) bool { return domain.FolderOf(it.Me.Preferences) != q.Folder })}
	if more && len(keys) > 0 {
		last := keys[len(keys)-1]
		p.Next = &last
	}
	return p, nil
}

func slotsOfKeys(keys []domain.RoomSortKey) []slot {
	out := make([]slot, len(keys))
	for i, k := range keys {
		out[i] = slot{room: k.Room}
	}
	return out
}

func slotsOfRows(rows []store.RoomOrderRow, full bool) []slot {
	out := make([]slot, len(rows))
	for i, r := range rows {
		out[i] = slot{room: r.Member.Room, activity: &r.Activity}
		if full {
			out[i].me = &r.Member
		}
	}
	return out
}

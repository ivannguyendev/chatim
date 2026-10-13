package roomlist

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type who struct{ tenant, user string }

type slot struct {
	room     uint64
	me       *domain.Member
	activity *store.RoomActivity
}

type firstRead struct {
	members    map[store.MemberKey]domain.Member
	asked      map[store.MemberKey]struct{}
	activities map[uint64]store.RoomActivity
	infos      map[uint64]domain.Room
}

type resolved struct {
	room     domain.Room
	me       domain.Member
	activity store.RoomActivity
	peer     store.MemberKey
	peerRead uint64
	peerDone bool
}

func (l *Lister) build(ctx context.Context, w who, slots []slot) ([]domain.RoomItem, []uint64, error) {
	first, err := l.firstWave(ctx, w, slots)
	if err != nil {
		return nil, nil, err
	}
	rooms, missing := first.resolve(w, slots)
	second, err := l.secondWave(ctx, w, rooms)
	if err != nil {
		return nil, nil, err
	}
	return second.items(w, rooms), missing, nil
}

func (l *Lister) firstWave(ctx context.Context, w who, slots []slot) (firstRead, error) {
	ids := make([]uint64, len(slots))
	for i, s := range slots {
		ids[i] = s.room
	}
	mark := l.info.Mark()
	infos, misses := l.info.Get(ids)
	keys, asked := memberKeysToRead(w, slots, infos)
	acts := roomsLackingActivity(slots)
	var (
		members  []domain.Member
		activity []store.RoomActivity
		loaded   []domain.Room
		g        errgroup.Group
	)
	if len(keys) > 0 {
		g.Go(func() (err error) {
			members, err = l.list.MembersByKeys(ctx, keys)
			return storeErr("read members", err)
		})
	}
	if len(acts) > 0 {
		g.Go(func() (err error) {
			activity, err = l.list.RoomActivities(ctx, acts)
			return storeErr("read room activity", err)
		})
	}
	if len(misses) > 0 {
		g.Go(func() (err error) {
			loaded, err = l.list.RoomInfos(ctx, misses)
			return storeErr("read room info", err)
		})
	}
	if err := g.Wait(); err != nil {
		return firstRead{}, err
	}
	l.info.Put(mark, loaded...)
	for _, r := range loaded {
		infos[r.ID] = r
	}
	out := firstRead{members: membersByKey(members), asked: asked, activities: make(map[uint64]store.RoomActivity, len(activity)), infos: infos}
	for _, a := range activity {
		out.activities[a.Room] = a
	}
	return out, nil
}

func (f firstRead) resolve(w who, slots []slot) ([]resolved, []uint64) {
	rooms := make([]resolved, 0, len(slots))
	var missing []uint64
	for _, s := range slots {
		r, ok := f.resolveOne(w, s)
		if !ok {
			missing = append(missing, s.room)
			continue
		}
		rooms = append(rooms, r)
	}
	return rooms, missing
}

func (f firstRead) resolveOne(w who, s slot) (resolved, bool) {
	me, ok := f.members[store.MemberKey{Room: s.room, User: w.user}]
	if s.me != nil {
		me, ok = *s.me, true
	}
	act, okAct := f.activities[s.room]
	if s.activity != nil {
		act, okAct = *s.activity, true
	}
	info, okInfo := f.infos[s.room]
	if !ok || !okAct || !okInfo || !belongs(w, s.room, me, info) {
		return resolved{}, false
	}
	r := resolved{room: info, me: me, activity: act}
	if peer, ok := peerOf(w, info); ok {
		r.peer = peer
		if _, done := f.asked[peer]; done {
			r.peerDone = true
			r.peerRead = peerReadSeq(w, f.members, peer)
		}
	}
	return r, true
}

func belongs(w who, room uint64, me domain.Member, info domain.Room) bool {
	return info.ID == room && info.Tenant == w.tenant &&
		me.Room == room && me.Tenant == w.tenant && me.User == w.user && me.Active()
}

func memberKeysToRead(w who, slots []slot, infos map[uint64]domain.Room) ([]store.MemberKey, map[store.MemberKey]struct{}) {
	var keys []store.MemberKey
	asked := map[store.MemberKey]struct{}{}
	for _, s := range slots {
		if s.me == nil {
			keys = append(keys, store.MemberKey{Room: s.room, User: w.user})
		}
		if info, ok := infos[s.room]; ok {
			if peer, ok := peerOf(w, info); ok {
				keys = append(keys, peer)
				asked[peer] = struct{}{}
			}
		}
	}
	return keys, asked
}

func roomsLackingActivity(slots []slot) []uint64 {
	var out []uint64
	for _, s := range slots {
		if s.activity == nil {
			out = append(out, s.room)
		}
	}
	return out
}

func membersByKey(members []domain.Member) map[store.MemberKey]domain.Member {
	out := make(map[store.MemberKey]domain.Member, len(members))
	for _, m := range members {
		out[store.MemberKey{Room: m.Room, User: m.User}] = m
	}
	return out
}

package roomlist

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type secondRead struct {
	tails  []roomcache.Tail
	hidden map[uint64][]uint64
	peers  map[store.MemberKey]domain.Member
}

func (l *Lister) secondWave(ctx context.Context, w who, rooms []resolved) (secondRead, error) {
	if len(rooms) == 0 {
		return secondRead{}, nil
	}
	var (
		out   secondRead
		peers []domain.Member
	)
	g, stop := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		out.tails, err = l.loadTails(ctx, stop, rooms)
		return err
	})
	g.Go(func() (err error) {
		out.hidden, err = l.hidden.HiddenInRooms(stop, w.user, hiddenRanges(rooms))
		return storeErr("read hidden messages", err)
	})
	if keys := peersToRead(rooms); len(keys) > 0 {
		g.Go(func() (err error) {
			peers, err = l.list.MembersByKeys(stop, keys)
			return storeErr("read direct peers", err)
		})
	}
	if err := g.Wait(); err != nil {
		return secondRead{}, err
	}
	out.peers = membersByKey(peers)
	return out, nil
}

func (s secondRead) items(w who, rooms []resolved) []domain.RoomItem {
	out := make([]domain.RoomItem, len(rooms))
	for i, r := range rooms {
		v := viewerOf(w, r.me, s.hidden[r.room.ID])
		tail := s.tails[i]
		preview, _ := domain.ChoosePreview(tail.Previews, v)
		peerRead := r.peerRead
		if r.peer.Room != 0 && !r.peerDone {
			peerRead = peerReadSeq(w, s.peers, r.peer)
		}
		out[i] = domain.RoomItem{Room: r.room, Me: r.me, Preview: preview, Unread: domain.CountUnread(tail.Window, v), PeerReadSeq: peerRead}
	}
	return out
}

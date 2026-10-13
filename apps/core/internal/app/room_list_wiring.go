package app

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomlist"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

const (
	roomInfoCacheEntries = 100_000
	roomTailCacheBytes   = 256 << 20
	roomSnapshotBytes    = 64 << 20
	roomPathCacheEntries = 100_000
	roomListJoinMaxRooms = 2_000
)

func wireRoomList(st *mongostore.Store) (*roomlist.Lister, error) {
	info, err := roomcache.NewInfoCache(roomInfoCacheEntries)
	if err != nil {
		return nil, fmt.Errorf("wire room info cache: %w", err)
	}
	tails, err := roomcache.NewTailCache(roomTailCacheBytes)
	if err != nil {
		return nil, fmt.Errorf("wire room tail cache: %w", err)
	}
	snaps, err := roomcache.NewSnapshotCache(roomSnapshotBytes)
	if err != nil {
		return nil, fmt.Errorf("wire room list snapshots: %w", err)
	}
	paths, err := roomcache.NewPathCache(roomPathCacheEntries)
	if err != nil {
		return nil, fmt.Errorf("wire room list paths: %w", err)
	}
	l, err := roomlist.New(roomlist.Deps{
		List: st, Tails: st, Hidden: st.Hidden(), Info: info, Tail: tails, Snapshots: snaps, Paths: paths, JoinMax: roomListJoinMaxRooms,
	})
	if err != nil {
		return nil, fmt.Errorf("wire room list: %w", err)
	}
	return l, nil
}

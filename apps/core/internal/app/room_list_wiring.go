package app

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomlist"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func wireRoomList(st *mongostore.Store, caches roomCaches, joinMax int) (*roomlist.Lister, error) {
	l, err := roomlist.New(roomlist.Deps{
		List: st, Tails: st, Hidden: st.Hidden(), Info: caches.info, Tail: caches.tails, Snapshots: caches.snaps, Paths: caches.paths, JoinMax: joinMax,
	})
	if err != nil {
		return nil, fmt.Errorf("wire room list: %w", err)
	}
	return l, nil
}

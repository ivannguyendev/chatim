package roomlist

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func peerOf(w who, info domain.Room) (store.MemberKey, bool) {
	if info.Tenant != w.tenant {
		return store.MemberKey{}, false
	}
	users := info.DirectUsers()
	if len(users) != 2 || (users[0] != w.user && users[1] != w.user) || users[0] == users[1] {
		return store.MemberKey{}, false
	}
	peer := users[0]
	if peer == w.user {
		peer = users[1]
	}
	return store.MemberKey{Room: info.ID, User: peer}, true
}

func peerReadSeq(w who, members map[store.MemberKey]domain.Member, peer store.MemberKey) uint64 {
	m, ok := members[peer]
	if !ok || m.Tenant != w.tenant || m.Room != peer.Room || m.User != peer.User {
		return 0
	}
	return m.ReadSeq
}

func peersToRead(rooms []resolved) []store.MemberKey {
	var keys []store.MemberKey
	for _, r := range rooms {
		if r.peer.Room != 0 && !r.peerDone {
			keys = append(keys, r.peer)
		}
	}
	return keys
}

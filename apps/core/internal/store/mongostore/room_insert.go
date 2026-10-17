package mongostore

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func roomInsert(r domain.Room, created bool) (flaggedDoc[roomDoc], error) {
	bits := store.BitRoomCreated
	if created {
		r.MemberCountVer, r.InfoVer = 1, 1
		bits |= store.BitMemberCount
	} else {
		r.MemberCount, r.MemberCountVer, r.InfoVer = 0, 0, 1
	}
	doc, err := encodeRoom(r)
	if err != nil {
		return flaggedDoc[roomDoc]{}, err
	}
	return flagDoc(doc, bits, insertFlagTime(r.CreatedAt)), nil
}

func founderInsert(r domain.Room, m domain.Member, at time.Time) (flaggedDoc[memberDoc], error) {
	doc, err := encodeMember(store.CreationMember(r, m))
	if err != nil {
		return flaggedDoc[memberDoc]{}, err
	}
	return flagDoc(doc, store.BitMember, at), nil
}

func insertFlagTime(at time.Time) time.Time {
	if at.IsZero() {
		at = time.Now()
	}
	return time.UnixMilli(at.UnixMilli()).UTC()
}

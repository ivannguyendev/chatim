package grpcsrv_test

import (
	"strconv"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func createdIDs(room uint64, users ...string) []string {
	ids := []string{pbconv.RoomCreatedEventID(room)}
	for _, u := range users {
		ids = append(ids, pbconv.MemberEventID(room, u, 1))
	}
	return append(ids, pbconv.MemberCountEventID(room, 1))
}

func eventIDs(events []*chatimv1.Event) []string {
	ids := make([]string, 0, len(events))
	for _, ev := range events {
		ids = append(ids, ev.GetId())
	}
	return ids
}

func manyUsers(creator string, n int) []string {
	users := []string{creator}
	for i := 1; i < n; i++ {
		users = append(users, "u"+strconv.Itoa(i))
	}
	return users
}

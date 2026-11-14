package projection

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const AvatarUsersName = "avatar_users"

type AvatarStore interface {
	RoomInfos(ctx context.Context, rooms []uint64) ([]domain.Room, error)
	store.AvatarMembers
}

type AvatarUsers struct {
	st AvatarStore
}

type avatarRoom struct {
	events  []int
	removed bool
}

func NewAvatarUsers(st AvatarStore) *AvatarUsers { return &AvatarUsers{st: st} }

func (a *AvatarUsers) Name() string { return AvatarUsersName }

func (a *AvatarUsers) Keeps(ev Event) bool {
	if ev.Proto.GetRoomType() != chatimv1.RoomType_ROOM_TYPE_GROUP {
		return false
	}
	switch ev.Proto.GetPayload().(type) {
	case *chatimv1.Event_MemberAdded, *chatimv1.Event_MemberRemoved:
		return true
	default:
		return false
	}
}

func (a *AvatarUsers) Handle(ctx context.Context, evs []Event) []error {
	errs := make([]error, len(evs))
	byRoom := make(map[uint64]*avatarRoom, len(evs))
	var order []uint64
	for i, ev := range evs {
		r, ok := byRoom[ev.Room]
		if !ok {
			r = &avatarRoom{}
			byRoom[ev.Room] = r
			order = append(order, ev.Room)
		}
		r.events = append(r.events, i)
		if _, gone := ev.Proto.GetPayload().(*chatimv1.Event_MemberRemoved); gone {
			r.removed = true
		}
	}
	for chunk := range slices.Chunk(order, store.RoomActivityBatch) {
		infos, err := a.st.RoomInfos(ctx, chunk)
		if err != nil {
			failRooms(errs, byRoom, chunk, fmt.Errorf("avatar users: %w", err))
			continue
		}
		for _, room := range infos {
			if room.Type != domain.RoomGroup {
				continue
			}
			if err := a.refresh(ctx, room, byRoom[room.ID].removed); err != nil {
				failRooms(errs, byRoom, []uint64{room.ID}, fmt.Errorf("avatar users of room %d: %w", room.ID, err))
			}
		}
	}
	return errs
}

func (a *AvatarUsers) refresh(ctx context.Context, room domain.Room, removed bool) error {
	if !removed && len(room.AvatarUsers) >= domain.MaxAvatarUsers {
		return nil
	}
	sample, err := a.st.MemberSample(ctx, room.ID, domain.MaxAvatarUsers)
	if err != nil {
		return err
	}
	if !removed && sameUsers(room.AvatarUsers, sample) {
		return nil
	}
	return a.st.SetAvatarUsers(ctx, room.ID, room.AvatarVer, sample)
}

func sameUsers(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

func failRooms(errs []error, byRoom map[uint64]*avatarRoom, rooms []uint64, err error) {
	for _, id := range rooms {
		if r, ok := byRoom[id]; ok {
			for _, i := range r.events {
				errs[i] = err
			}
		}
	}
}

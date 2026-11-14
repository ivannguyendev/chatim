package app

import (
	"context"
	"errors"
	"slices"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
)

type roomGetter interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}

type mediaRoomAvatars struct {
	rooms roomGetter
}

func (r mediaRoomAvatars) RoomAvatars(ctx context.Context, room string) (mediaapp.RoomAvatarState, error) {
	id, err := mediaRoomID(room)
	if err != nil {
		return mediaapp.RoomAvatarState{}, err
	}
	got, err := r.rooms.Get(ctx, id)
	switch {
	case errors.Is(err, domain.ErrRoomNotFound):
		return mediaapp.RoomAvatarState{}, nil
	case err != nil:
		return mediaapp.RoomAvatarState{}, err
	}
	return mediaapp.RoomAvatarState{
		Found: true, Tenant: got.Tenant, Group: got.Type == domain.RoomGroup, Keys: slices.Clone(got.Avatars), InfoVer: got.InfoVer,
	}, nil
}

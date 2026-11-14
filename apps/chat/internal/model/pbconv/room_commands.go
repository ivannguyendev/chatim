package pbconv

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func DomainPreferencesChange(req *chatimv1.SetRoomPreferencesRequest) domain.PreferencesChange {
	var c domain.PreferencesChange
	if req.Pinned != nil {
		c.Pinned = new(req.GetPinned())
	}
	if req.Archived != nil {
		c.Archived = new(req.GetArchived())
	}
	if req.GetMutedUntil() != nil {
		c.MutedUntil = new(req.GetMutedUntil().AsTime())
	}
	return c
}

func DomainRoomUpdate(req *chatimv1.UpdateRoomRequest) domain.RoomUpdate {
	var u domain.RoomUpdate
	if req.Name != nil {
		u.Name = new(req.GetName())
	}
	if req.Avatars != nil {
		u.Avatars = new(slices.Clone(req.GetAvatars().GetKeys()))
	}
	return u
}

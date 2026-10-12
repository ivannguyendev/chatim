package pbconv

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MessageChanged(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	if e.Kind == domain.EditDelete {
		return MessageDeleted(roomType, m, e)
	}
	return MessageEdited(roomType, m, e)
}

package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type RoomAvatars struct {
	mu   sync.Mutex
	docs map[string]domain.RoomAvatar
}

func NewRoomAvatars() *RoomAvatars {
	return &RoomAvatars{docs: map[string]domain.RoomAvatar{}}
}

func (s *RoomAvatars) Get(_ context.Context, room string) (domain.RoomAvatar, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[room]
	return doc, ok, nil
}

func (s *RoomAvatars) Put(_ context.Context, next domain.RoomAvatar, prevVer uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if had, ok := s.docs[next.Room]; ok && had.InfoVer != prevVer {
		return domain.ErrRetryLater
	}
	next.UpdatedAt = next.UpdatedAt.Truncate(time.Millisecond).UTC()
	s.docs[next.Room] = next
	return nil
}

func (s *RoomAvatars) InUse(_ context.Context, fileKey string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, doc := range s.docs {
		if doc.FileKey == fileKey {
			return true, nil
		}
	}
	return false, nil
}

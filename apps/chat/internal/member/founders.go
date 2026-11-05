package member

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
)

func (s *Service) AddFounders(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error) {
	return s.d.Members.AddMembers(ctx, j, users)
}

func (s *Service) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	return s.d.Members.MembersOf(ctx, room, users)
}

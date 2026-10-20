package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) GetReactionSettings(ctx context.Context, _ *chatimv1.GetReactionSettingsRequest) (*chatimv1.GetReactionSettingsResponse, error) {
	if _, err := callerid.Of(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.GetReactionSettingsResponse{Emojis: s.interactionService.ReactionEmojis()}, nil
}

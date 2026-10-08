package grpcsrv

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) GetReactionSettings(ctx context.Context, _ *chatimv1.GetReactionSettingsRequest) (*chatimv1.GetReactionSettingsResponse, error) {
	if _, err := callerOf(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.GetReactionSettingsResponse{Emojis: s.mutator.ReactionEmojis()}, nil
}

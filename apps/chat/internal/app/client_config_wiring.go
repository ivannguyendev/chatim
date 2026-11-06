package app

import (
	"github.com/ivannguyendev/chatim/apps/chat/internal/config"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func clientConfig(cfg config.Config, media *mediaapp.App) func() *chatimv1.GetClientConfigResponse {
	limits := &chatimv1.MessageLimits{
		MaxTextBytes: domain.MaxTextBytes, MaxMetaBytes: domain.MaxMetaBytes,
		MaxMentions: int64(cfg.Messages.MentionTargets), MaxAttachments: domain.MaxAttachments,
	}
	mention := &chatimv1.MentionFeature{Enabled: cfg.Mention.Enabled}
	return func() *chatimv1.GetClientConfigResponse {
		feature := &chatimv1.MediaFeature{}
		if media != nil {
			c := media.ClientConfig()
			feature = &chatimv1.MediaFeature{Enabled: c.Enabled, MaxFileBytes: c.MaxFileBytes, MaxParallelUploads: int64(c.MaxParallelUploads)}
		}
		return &chatimv1.GetClientConfigResponse{Message: limits, Mention: mention, Media: feature}
	}
}

package mediaapp

import (
	"context"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type (
	MessageRef   = domain.MessageRef
	MessageFiles = domain.MessageFiles
	File         = domain.File
	MessageState = domain.MessageState
)

type ChatReader interface {
	VisibleAttachments(ctx context.Context, tenant, user string, refs []MessageRef) ([]MessageFiles, error)
	MessageFiles(ctx context.Context, ref MessageRef) (MessageState, error)
}

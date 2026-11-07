package mediaapp

import (
	"context"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type (
	MessageRef   = domain.MessageRef
	MessageFiles = domain.MessageFiles
	File         = domain.File
)

type ChatReader interface {
	VisibleAttachments(ctx context.Context, tenant, user string, refs []MessageRef) ([]MessageFiles, error)
}

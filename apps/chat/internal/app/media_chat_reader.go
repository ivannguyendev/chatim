package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

var errMediaReaderUnbound = fmt.Errorf("chat reader for media is not bound yet: %w", apperr.ErrUnavailable)

type mediaChatNode struct {
	reader atomic.Pointer[visibleReader]
}

var _ mediaapp.ChatReader = (*mediaChatNode)(nil)

func (c *mediaChatNode) bind(r visibleReader) { c.reader.Store(&r) }

func (c *mediaChatNode) VisibleAttachments(ctx context.Context, tenant, user string, refs []mediaapp.MessageRef) ([]mediaapp.MessageFiles, error) {
	r := c.reader.Load()
	if r == nil {
		return nil, errMediaReaderUnbound
	}
	keys, err := mediaMessageKeys(refs)
	if err != nil {
		return nil, err
	}
	visible, err := (*r).VisibleMessages(ctx, tenant, user, keys)
	if err != nil {
		return nil, err
	}
	out := make([]mediaapp.MessageFiles, 0, len(visible))
	for _, k := range keys {
		if m, ok := visible[k]; ok && len(m.Attachments) > 0 {
			out = append(out, mediaapp.MessageFiles{
				Ref:   mediaapp.MessageRef{Room: ids.FormatRoomID(k.Room), Thread: k.Thread, Seq: k.Seq},
				Files: mediaFiles(m.Attachments),
			})
		}
	}
	return out, nil
}

func mediaMessageKeys(refs []mediaapp.MessageRef) ([]store.MsgKey, error) {
	seen := make(map[store.MsgKey]struct{}, len(refs))
	out := make([]store.MsgKey, 0, len(refs))
	for _, ref := range refs {
		room, err := ids.ParseRoomID(ref.Room)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", apperr.ErrInvalidArgument, err)
		}
		if ref.Seq == 0 {
			return nil, fmt.Errorf("%w: message seq must be at least 1", apperr.ErrInvalidArgument)
		}
		k := store.MsgKey{Room: room, Thread: ref.Thread, Seq: ref.Seq}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, nil
}

func mediaFiles(list []domain.Attachment) []mediaapp.File {
	out := make([]mediaapp.File, len(list))
	for i, a := range list {
		out[i] = mediaapp.File{Key: a.Key, Name: a.Name, Mime: a.Mime}
	}
	return out
}

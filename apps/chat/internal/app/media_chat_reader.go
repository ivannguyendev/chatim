package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

var errMediaReaderUnbound = fmt.Errorf("chat reader for media is not bound yet: %w", apperr.ErrUnavailable)

type mediaReader interface {
	visibleReader
	ReadAccess(ctx context.Context, tenant, user string, room uint64) (access.Request, error)
}

type mediaChatNode struct {
	mediaMessageFiles
	mediaRoomAvatars
	reader atomic.Pointer[mediaReader]
}

var _ mediaapp.ChatReader = (*mediaChatNode)(nil)

func (c *mediaChatNode) bind(r mediaReader) { c.reader.Store(&r) }

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
				Ref:    mediaapp.MessageRef{Room: ids.FormatRoomID(k.Room), Thread: k.Thread, Seq: k.Seq},
				Sender: m.From, SentAt: m.CreatedAt, Files: mediaFiles(m.Attachments),
			})
		}
	}
	return out, nil
}

func (c *mediaChatNode) RoomReadAccess(ctx context.Context, tenant, user, room string) (mediaapp.RoomAccess, error) {
	r := c.reader.Load()
	if r == nil {
		return mediaapp.RoomAccess{}, errMediaReaderUnbound
	}
	id, err := mediaRoomID(room)
	if err != nil {
		return mediaapp.RoomAccess{}, err
	}
	grant, err := (*r).ReadAccess(ctx, tenant, user, id)
	if err != nil {
		return mediaapp.RoomAccess{}, err
	}
	return mediaapp.RoomAccess{Room: ids.FormatRoomID(id), ClearedAt: grant.Member.ClearedAt}, nil
}

func mediaRoomID(room string) (uint64, error) {
	id, err := ids.ParseRoomID(room)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", apperr.ErrInvalidArgument, err)
	}
	return id, nil
}

func mediaMessageKey(ref mediaapp.MessageRef) (store.MsgKey, error) {
	room, err := mediaRoomID(ref.Room)
	if err != nil {
		return store.MsgKey{}, err
	}
	if ref.Seq == 0 {
		return store.MsgKey{}, fmt.Errorf("%w: message seq must be at least 1", apperr.ErrInvalidArgument)
	}
	return store.MsgKey{Room: room, Thread: ref.Thread, Seq: ref.Seq}, nil
}

func mediaMessageKeys(refs []mediaapp.MessageRef) ([]store.MsgKey, error) {
	seen := make(map[store.MsgKey]struct{}, len(refs))
	out := make([]store.MsgKey, 0, len(refs))
	for _, ref := range refs {
		k, err := mediaMessageKey(ref)
		if err != nil {
			return nil, err
		}
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
		out[i] = mediaapp.File{Key: a.Key, Name: a.Name, Mime: a.Mime, Size: a.Size, Dim: a.Dim, Dur: a.Dur, Hash: a.Hash}
	}
	return out
}

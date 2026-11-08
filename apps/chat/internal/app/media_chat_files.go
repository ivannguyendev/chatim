package app

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
)

type mediaMessageFiles struct {
	messages store.Messages
}

func (r mediaMessageFiles) MessageFiles(ctx context.Context, ref mediaapp.MessageRef) (mediaapp.MessageState, error) {
	k, err := mediaMessageKey(ref)
	if err != nil {
		return mediaapp.MessageState{}, err
	}
	found, err := r.messages.Find(ctx, k.Room, []store.MsgKey{k})
	if err != nil {
		return mediaapp.MessageState{}, err
	}
	if len(found) == 0 {
		return mediaapp.MessageState{}, nil
	}
	m := found[0]
	state := mediaapp.MessageState{Found: true, Deleted: m.Deleted, Tenant: m.Tenant}
	if !m.Deleted {
		state.Keys = attachmentKeys(m.Attachments)
	}
	return state, nil
}

func attachmentKeys(list []domain.Attachment) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = a.Key
	}
	return out
}

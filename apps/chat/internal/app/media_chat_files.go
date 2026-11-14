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
	state := mediaapp.MessageState{Found: true, Deleted: m.Deleted, Tenant: m.Tenant, SentAt: m.CreatedAt}
	if !m.Deleted {
		state.Keys, state.Mimes = attachmentKeys(m.Attachments)
	}
	return state, nil
}

func attachmentKeys(list []domain.Attachment) ([]string, map[string]string) {
	if len(list) == 0 {
		return nil, nil
	}
	keys := make([]string, len(list))
	mimes := make(map[string]string, len(list))
	for i, a := range list {
		keys[i], mimes[a.Key] = a.Key, a.Mime
	}
	return keys, mimes
}

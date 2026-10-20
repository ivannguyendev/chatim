package app

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c mentionCore) VisibleMessages(ctx context.Context, tenant, user string, keys []mentionapp.MessageKey) ([]*chatimv1.Message, error) {
	ks := storeKeys(keys)
	visible, err := c.visible.VisibleMessages(ctx, tenant, user, ks)
	if err != nil {
		return nil, err
	}
	out := make([]*chatimv1.Message, 0, len(visible))
	for _, k := range ks {
		if m, ok := visible[k]; ok {
			out = append(out, pbconv.Message(m))
			delete(visible, k)
		}
	}
	return out, nil
}

func lookupInChunks[V any](ctx context.Context, keys []mentionapp.MessageKey, read func(context.Context, []store.MsgKey) (map[store.MsgKey]V, error)) (map[mentionapp.MessageKey]V, error) {
	out := make(map[mentionapp.MessageKey]V, len(keys))
	for start := 0; start < len(keys); start += store.MaxMessageLookup {
		got, err := read(ctx, storeKeys(keys[start:min(len(keys), start+store.MaxMessageLookup)]))
		if err != nil {
			return nil, err
		}
		for k, v := range got {
			out[mentionapp.MessageKey(k)] = v
		}
	}
	return out, nil
}

func storeKeys(keys []mentionapp.MessageKey) []store.MsgKey {
	out := make([]store.MsgKey, len(keys))
	for i, k := range keys {
		out[i] = store.MsgKey(k)
	}
	return out
}

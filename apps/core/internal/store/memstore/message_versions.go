package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MessageVersions = (*Messages)(nil)

func (s *Messages) MessageVersions(ctx context.Context, keys []store.MsgKey) (map[store.MsgKey]uint32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMessageLookup(keys); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[store.MsgKey]uint32, len(keys))
	for _, k := range keys {
		line := s.lines[timeline{k.Room, k.Thread}]
		if i, ok := slices.BinarySearchFunc(line, k.Seq, bySeq); ok {
			out[k] = line[i].Version
		}
	}
	return out, nil
}

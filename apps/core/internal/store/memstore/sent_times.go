package memstore

import (
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MessageTimes = (*Messages)(nil)

func (s *Messages) SentTimes(ctx context.Context, keys []store.MsgKey) (map[store.MsgKey]time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateSentTimeKeys(keys); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[store.MsgKey]time.Time, len(keys))
	for _, k := range keys {
		line := s.lines[timeline{k.Room, k.Thread}]
		if i, ok := slices.BinarySearchFunc(line, k.Seq, bySeq); ok {
			out[k] = line[i].CreatedAt
		}
	}
	return out, nil
}

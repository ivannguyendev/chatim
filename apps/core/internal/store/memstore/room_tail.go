package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.RoomTails = (*Messages)(nil)

func (s *Messages) TailWindow(ctx context.Context, room, afterSeq uint64, limit int) ([]domain.TailMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateTailWindow(room, afterSeq, limit); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.lines[timeline{room, 0}]
	start, found := slices.BinarySearchFunc(line, afterSeq, bySeq)
	if found {
		start++
	}
	start = max(start, len(line)-limit)
	out := make([]domain.TailMessage, 0, len(line)-start)
	for _, m := range slices.Backward(line[start:]) {
		out = append(out, domain.TailMessage{Seq: m.Seq, From: m.From, Deleted: m.Deleted, SkipUnread: m.SkipUnread, CreatedAt: m.CreatedAt})
	}
	return out, nil
}

func (s *Messages) TailPreviews(ctx context.Context, keys []store.MsgKey) ([]domain.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateTailPreviews(keys); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Message{}
	for _, k := range keys {
		line := s.lines[timeline{k.Room, k.Thread}]
		if i, ok := slices.BinarySearchFunc(line, k.Seq, bySeq); ok {
			out = append(out, detached(line[i]))
		}
	}
	store.SortByKey(out)
	return slices.CompactFunc(out, func(a, b domain.Message) bool { return store.KeyOf(a) == store.KeyOf(b) }), nil
}

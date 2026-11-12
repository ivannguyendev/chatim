package memstore

import (
	"context"
	"slices"
	"strings"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func (s *MessageFiles) ListRoom(_ context.Context, q domain.RoomFilesQuery) ([]domain.MessageFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type listed struct {
		id   string
		pair domain.MessageFile
	}
	var found []listed
	for id, p := range s.pairs {
		if inRoomPage(q, id, p) {
			found = append(found, listed{id: id, pair: p})
		}
	}
	slices.SortFunc(found, func(a, b listed) int {
		if c := b.pair.SentAt.Compare(a.pair.SentAt); c != 0 {
			return c
		}
		return strings.Compare(b.id, a.id)
	})
	out := make([]domain.MessageFile, 0, min(len(found), q.Limit))
	for _, f := range found[:min(len(found), q.Limit)] {
		out = append(out, f.pair)
	}
	return out, nil
}

func inRoomPage(q domain.RoomFilesQuery, id string, p domain.MessageFile) bool {
	if p.Tenant != q.Tenant || p.Room != q.Room || p.Kind != q.Kind || p.State != domain.PairLive {
		return false
	}
	newerThanCleared := q.ClearedAt.IsZero() || p.SentAt.After(q.ClearedAt)
	if q.Before.IsZero() {
		return newerThanCleared
	}
	if p.SentAt.Before(q.Before.SentAt) {
		return newerThanCleared
	}
	return p.SentAt.Equal(q.Before.SentAt) && id < q.Before.MessageKey+"|"+q.Before.FileKey
}

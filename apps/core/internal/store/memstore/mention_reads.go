package memstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Mentions) MarkRead(ctx context.Context, room uint64, user string, readSeq uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateMentionRead(room, user, readSeq); err != nil {
		return err
	}
	target := domain.MentionTarget{Kind: domain.MentionUser, ID: user}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, docs := range s.docs {
		if d, ok := docs[target]; ok && d.Live && key.Room == room && key.Thread == 0 {
			d.Read = key.Seq <= readSeq
			docs[target] = d
		}
	}
	return nil
}

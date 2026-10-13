package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type MyInteractionReader interface {
	MyInteractions(ctx context.Context, user string, keys []store.MsgKey) (map[store.MsgKey]store.MyInteraction, error)
}

func (s *Service) readerMessages(ctx context.Context, viewer view.Viewer, room uint64, page []domain.Message) ([]*chatimv1.Message, error) {
	out := make([]*chatimv1.Message, len(page))
	for i, m := range page {
		out[i] = pbconv.Message(m)
	}
	if err := s.addReplyPreviews(ctx, viewer, room, page, out); err != nil {
		return nil, err
	}
	if err := s.addMyInteractions(ctx, viewer.User, page, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) addMyInteractions(ctx context.Context, user string, page []domain.Message, out []*chatimv1.Message) error {
	var asked []store.MsgKey
	for _, m := range page {
		if visibleToReader(m) {
			asked = append(asked, store.KeyOf(m))
		}
	}
	if len(asked) == 0 {
		return nil
	}
	mine, err := s.mine.MyInteractions(ctx, user, asked)
	if err != nil {
		return err
	}
	for i, m := range page {
		if x, ok := mine[store.KeyOf(m)]; ok && visibleToReader(m) {
			out[i].MyReaction, out[i].Bookmarked = x.Reaction, x.Bookmarked
		}
	}
	return nil
}

func visibleToReader(m domain.Message) bool { return !m.Deleted && !m.Hidden }

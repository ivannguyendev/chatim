package interaction

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type BookmarkCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	On                bool
}

func (s *Service) SetBookmark(ctx context.Context, c BookmarkCmd) (bool, error) {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return false, err
	}
	req, msg, err := s.d.Access.Target(ctx, s.d.Messages, access.SetBookmark, c.Tenant, c.User, key)
	if err != nil {
		return false, err
	}
	if c.On && msg.Deleted {
		return false, domain.ErrMessageDeleted
	}
	doc, changed, err := s.d.Interactions.SetBookmark(ctx, domain.Bookmark{
		Room: c.Room, Thread: c.Thread, Seq: c.Seq, Tenant: c.Tenant, User: c.User, On: c.On, At: s.now(),
	})
	if err != nil || !changed {
		return false, err
	}
	s.tell(c.Room, pbconv.BookmarkChanged(req.Room, doc))
	return true, nil
}

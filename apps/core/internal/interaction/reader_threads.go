package interaction

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ThreadItem struct {
	Parent      domain.Message
	LastReplyAt time.Time
	LastReply   *domain.Message
}

type ThreadPage struct {
	Items []ThreadItem
	Next  *store.ReplyThread
}

func (r *Reader) Threads(ctx context.Context, tenant, user string, room uint64, before store.ReplyThreadCursor, limit int) (ThreadPage, error) {
	n, err := domain.PageLimit(limit)
	if err != nil {
		return ThreadPage{}, err
	}
	grant, err := r.d.Access.Authorize(ctx, access.ReadReplies, tenant, user, room)
	if err != nil {
		return ThreadPage{}, err
	}
	threads, err := r.d.Threads.List(ctx, room, before, n)
	if err != nil {
		return ThreadPage{}, err
	}
	items, err := r.threadItems(ctx, user, grant, threads)
	if err != nil {
		return ThreadPage{}, err
	}
	page := ThreadPage{Items: items}
	if len(threads) == n {
		page.Next = &threads[len(threads)-1]
	}
	return page, nil
}

func (r *Reader) threadItems(ctx context.Context, user string, grant access.Request, threads []store.ReplyThread) ([]ThreadItem, error) {
	if len(threads) == 0 {
		return nil, nil
	}
	asked := make(map[store.MsgKey]bool, 2*len(threads))
	keys := make([]store.MsgKey, 0, 2*len(threads))
	for _, t := range threads {
		for _, k := range []store.MsgKey{t.Parent, {Room: t.Parent.Room, Seq: t.LastReplySeq}} {
			if !asked[k] {
				asked[k] = true
				keys = append(keys, k)
			}
		}
	}
	found, err := r.d.Messages.Find(ctx, grant.Room.ID, keys)
	if err != nil {
		return nil, err
	}
	masked, err := r.d.Views.Masked(ctx, user, grant, grant.Room.ID, 0, found)
	if err != nil {
		return nil, err
	}
	raw, shown := byKey(found), byKey(masked)
	items := make([]ThreadItem, 0, len(threads))
	for _, t := range threads {
		if parent, ok := raw[t.Parent]; !ok || parent.Replies.N == 0 {
			continue
		}
		item := ThreadItem{Parent: shown[t.Parent], LastReplyAt: t.LastReplyAt}
		if reply, ok := shown[store.MsgKey{Room: t.Parent.Room, Seq: t.LastReplySeq}]; ok {
			item.LastReply = &reply
		}
		items = append(items, item)
	}
	return items, nil
}

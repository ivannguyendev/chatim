package projection

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (x *MentionReplyIndex) link(ctx context.Context, c messageChange) error {
	m := c.msg
	parent := eventkit.MessageKey(m.Room, m.ReplyTo.Thread, m.ReplyTo.Seq)
	reply := domain.Reply{Parent: domain.MsgKey(parent), Room: m.Room, Thread: m.Thread, Seq: m.Seq, Tenant: m.Tenant, From: m.From, At: m.CreatedAt}
	if err := store.ValidateReply(reply); err != nil {
		return err
	}
	if err := x.count(ctx, c, parent, reply); err != nil || m.Deleted {
		return err
	}
	return x.deps.Threads.Touch(ctx, parent, m.Tenant, m.Seq, m.CreatedAt)
}

func (x *MentionReplyIndex) count(ctx context.Context, c messageChange, parent store.MsgKey, reply domain.Reply) error {
	tm, err := x.deps.Timers.ArmMessageCountCheck(ctx, parent, pbconv.RepliesCounter)
	if err != nil {
		return err
	}
	changed, delta, err := x.writeReply(ctx, c, reply)
	switch {
	case err != nil:
		return err
	case !changed:
		x.deps.Timers.Disarm(ctx, tm)
		return nil
	}
	rc, err := x.deps.Counts.AddReplyCount(ctx, parent, delta)
	switch {
	case err == nil:
		x.announce(c, parent, rc)
	case !eventkit.Gone(err):
		return err
	}
	x.deps.Timers.Disarm(ctx, tm)
	return err
}

func (x *MentionReplyIndex) writeReply(ctx context.Context, c messageChange, reply domain.Reply) (bool, int, error) {
	if !c.msg.Deleted {
		added, err := x.deps.Replies.AddReply(ctx, reply)
		return added, 1, err
	}
	at := c.msg.EditedAt
	if at.IsZero() {
		at = c.at
	}
	removed, err := x.deps.Replies.RemoveReply(ctx, reply, at)
	return removed, -1, err
}

func (x *MentionReplyIndex) announce(c messageChange, parent store.MsgKey, rc domain.ReplyCount) {
	typ, err := pbconv.DomainRoomType(c.roomType)
	if err != nil {
		return
	}
	m := domain.Message{Room: parent.Room, Thread: parent.Thread, Seq: parent.Seq, Tenant: c.msg.Tenant, Replies: rc}
	_ = x.deps.Events.Enqueue(parent.Room, []*chatimv1.Event{pbconv.ReplyCountsChanged(typ, m, x.now())})
}

package effects

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (e *ReplyMentionIndex) link(ctx context.Context, m domain.Message) error {
	parent := store.MsgKey{Room: m.Room, Thread: m.ReplyTo.Thread, Seq: m.ReplyTo.Seq}
	reply := domain.Reply{Parent: domain.MsgKey(parent), Room: m.Room, Thread: m.Thread, Seq: m.Seq, Tenant: m.Tenant, From: m.From, At: m.CreatedAt}
	if err := store.ValidateReply(reply); err != nil {
		return err
	}
	tm, err := e.deps.Timers.ArmMessageCountCheck(ctx, parent, pbconv.RepliesCounter)
	if err != nil {
		return err
	}
	changed, delta, err := e.writeReply(ctx, m, reply)
	switch {
	case err != nil:
		return err
	case !changed:
		e.deps.Timers.Disarm(ctx, tm)
		return nil
	}
	rc, err := e.deps.Counts.AddReplyCount(ctx, parent, delta)
	if err == nil {
		err = e.announceReplies(ctx, parent, m.Tenant, rc)
	}
	if err != nil && !gone(err) {
		return err
	}
	e.deps.Timers.Disarm(ctx, tm)
	return err
}

func (e *ReplyMentionIndex) writeReply(ctx context.Context, m domain.Message, reply domain.Reply) (bool, int, error) {
	if !m.Deleted {
		added, err := e.deps.Replies.AddReply(ctx, reply)
		return added, 1, err
	}
	at := m.EditedAt
	if at.IsZero() {
		at = e.now()
	}
	removed, err := e.deps.Replies.RemoveReply(ctx, reply, at)
	return removed, -1, err
}

func (e *ReplyMentionIndex) announceReplies(ctx context.Context, parent store.MsgKey, tenant string, rc domain.ReplyCount) error {
	typ, err := e.types.get(ctx, parent.Room)
	if err != nil {
		return err
	}
	m := domain.Message{Room: parent.Room, Thread: parent.Thread, Seq: parent.Seq, Tenant: tenant, Replies: rc}
	return e.publishOne(ctx, parent.Room, pbconv.ReplyCountsChanged(typ, m, e.now()))
}

func (e *ReplyMentionIndex) now() time.Time { return e.deps.Now().UTC().Truncate(time.Millisecond) }

package projection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MentionReplyIndexName = "mention_reply_index"

var messageChangeKinds = [...]string{"msg_created", "msg_edited", "msg_deleted"}

type MentionReplyIndexDeps struct {
	Replies  ReplyLinks
	Threads  ReplyThreadWriter
	Counts   ReplyCounter
	Mentions MentionWriter
	Shared   SharedMentionRaiser
	Timers   LinkTimers
	Events   EventQueue
	Now      func() time.Time
}

type MentionReplyIndex struct {
	deps MentionReplyIndexDeps
}

type messageChange struct {
	msg      domain.Message
	created  bool
	roomType chatimv1.RoomType
	at       time.Time
}

func NewMentionReplyIndex(deps MentionReplyIndexDeps) (*MentionReplyIndex, error) {
	if !deps.complete() {
		return nil, fmt.Errorf("%w: %s needs replies, reply threads, counts, mentions, shared mentions, timers and an event queue", apperr.ErrInvalidArgument, MentionReplyIndexName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &MentionReplyIndex{deps: deps}, nil
}

func (d MentionReplyIndexDeps) complete() bool {
	return d.Replies != nil && d.Threads != nil && d.Counts != nil && d.Mentions != nil && d.Shared != nil && d.Timers != nil && d.Events != nil
}

func (x *MentionReplyIndex) Name() string { return MentionReplyIndexName }

func (x *MentionReplyIndex) Subjects(root string) []string {
	out := make([]string, len(messageChangeKinds))
	for i, kind := range messageChangeKinds {
		out[i] = filter(root, "message", kind)
	}
	return out
}

func (x *MentionReplyIndex) Handle(ctx context.Context, evs []Event) []error {
	errs := make([]error, len(evs))
	for i, ev := range evs {
		errs[i] = x.index(ctx, ev)
	}
	return errs
}

func (x *MentionReplyIndex) index(ctx context.Context, ev Event) error {
	c, err := messageChangeOf(ev)
	if err != nil {
		return err
	}
	var replyErr, mentionErr error
	if c.msg.ReplyTo != nil && (c.created || c.msg.Deleted) {
		replyErr = x.link(ctx, c)
	}
	if !c.created || c.msg.Deleted || store.ReplyMentionFlagsOf(c.msg)&store.HasMention != 0 {
		mentionErr = x.mention(ctx, c.msg)
	}
	return settled(replyErr, mentionErr)
}

func settled(errs ...error) error {
	var failed []error
	var gone error
	for _, err := range errs {
		switch {
		case err == nil:
		case eventkit.Gone(err):
			gone = err
		default:
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		return errors.Join(failed...)
	}
	return gone
}

func messageChangeOf(ev Event) (messageChange, error) {
	c := messageChange{roomType: ev.Proto.GetRoomType(), at: ev.At}
	var pm *chatimv1.Message
	switch p := ev.Proto.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		pm, c.created = p.MessageCreated.GetMessage(), true
	case *chatimv1.Event_MessageEdited:
		pm = p.MessageEdited.GetMessage()
	case *chatimv1.Event_MessageDeleted:
		pm = p.MessageDeleted.GetMessage()
	default:
		return messageChange{}, fmt.Errorf("%w: %s does not keep %T", apperr.ErrInvalidArgument, MentionReplyIndexName, ev.Proto.GetPayload())
	}
	if pm == nil || pm.GetCreatedAt().CheckValid() != nil {
		return messageChange{}, fmt.Errorf("%w: event %s carries no message with a send time", apperr.ErrInvalidArgument, ev.Proto.GetId())
	}
	c.msg = domain.Message{
		Room: ev.Room, Thread: pm.GetThreadRoot(), Seq: pm.GetSeq(), Tenant: ev.Proto.GetTenant(), From: pm.GetSender(),
		CreatedAt: pm.GetCreatedAt().AsTime(), Version: pm.GetVer(), Deleted: pm.GetDeleted(),
		ReplyTo: pbconv.DomainReplyRef(pm.GetReplyTo()), Mentions: pbconv.DomainMentionTargets(pm.GetMentionTargets()), MentionAll: pm.GetMentionAll(),
	}
	if pm.GetEditedAt().CheckValid() == nil {
		c.msg.EditedAt = pm.GetEditedAt().AsTime()
	}
	return c, nil
}

func (x *MentionReplyIndex) now() time.Time { return x.deps.Now().UTC().Truncate(time.Millisecond) }

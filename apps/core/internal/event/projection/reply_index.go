package projection

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ReplyIndexName = "reply_index"

type ReplyIndexDeps struct {
	Replies ReplyLinks
	Threads ReplyThreadWriter
	Counts  ReplyCounter
	Timers  LinkTimers
	Events  EventQueue
	Now     func() time.Time
}

type ReplyIndex struct {
	deps ReplyIndexDeps
}

type messageChange struct {
	msg      domain.Message
	created  bool
	roomType chatimv1.RoomType
	at       time.Time
}

func NewReplyIndex(deps ReplyIndexDeps) (*ReplyIndex, error) {
	if deps.Replies == nil || deps.Threads == nil || deps.Counts == nil || deps.Timers == nil || deps.Events == nil {
		return nil, fmt.Errorf("%w: %s needs replies, reply threads, counts, timers and an event queue", apperr.ErrInvalidArgument, ReplyIndexName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &ReplyIndex{deps: deps}, nil
}

func (x *ReplyIndex) Name() string { return ReplyIndexName }

func (x *ReplyIndex) Keeps(ev Event) bool {
	switch ev.Proto.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated, *chatimv1.Event_MessageDeleted:
		return true
	default:
		return false
	}
}

func (x *ReplyIndex) Handle(ctx context.Context, evs []Event) []error {
	errs := make([]error, len(evs))
	for i, ev := range evs {
		errs[i] = x.index(ctx, ev)
	}
	return errs
}

func (x *ReplyIndex) index(ctx context.Context, ev Event) error {
	c, err := messageChangeOf(ev)
	if err != nil {
		return err
	}
	if c.msg.ReplyTo == nil || (!c.created && !c.msg.Deleted) {
		return nil
	}
	return x.link(ctx, c)
}

func messageChangeOf(ev Event) (messageChange, error) {
	c := messageChange{roomType: ev.Proto.GetRoomType(), at: ev.At}
	var pm *chatimv1.Message
	switch p := ev.Proto.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		pm, c.created = p.MessageCreated.GetMessage(), true
	case *chatimv1.Event_MessageDeleted:
		pm = p.MessageDeleted.GetMessage()
	default:
		return messageChange{}, fmt.Errorf("%w: %s does not keep %T", apperr.ErrInvalidArgument, ReplyIndexName, ev.Proto.GetPayload())
	}
	if pm == nil || pm.GetCreatedAt().CheckValid() != nil {
		return messageChange{}, fmt.Errorf("%w: event %s carries no message with a send time", apperr.ErrInvalidArgument, ev.Proto.GetId())
	}
	c.msg = domain.Message{
		Room: ev.Room, Thread: pm.GetThreadRoot(), Seq: pm.GetSeq(), Tenant: ev.Proto.GetTenant(), From: pm.GetSender(),
		CreatedAt: pm.GetCreatedAt().AsTime(), Version: pm.GetVer(), Deleted: pm.GetDeleted(), ReplyTo: pbconv.DomainReplyRef(pm.GetReplyTo()),
	}
	if pm.GetEditedAt().CheckValid() == nil {
		c.msg.EditedAt = pm.GetEditedAt().AsTime()
	}
	return c, nil
}

func (x *ReplyIndex) now() time.Time { return x.deps.Now().UTC().Truncate(time.Millisecond) }

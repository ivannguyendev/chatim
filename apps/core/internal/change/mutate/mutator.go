package mutate

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, rooms, events, reactions, reaction counts, message count timers, pins, a pin projector, members, request dedupe, a member forgetter, member count timers and read positions", apperr.ErrInvalidArgument)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	Last(ctx context.Context, room, thread uint64) (uint64, error)
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, bool, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access       *access.Checker
	Messages     Messages
	Edits        store.Edits
	Hidden       store.Hidden
	Rooms        HistoryClearer
	Events       EventPublisher
	Interactions store.Interactions
	Counts       ReactionCounts
	CountTimers  MessageCountTimers
	Pins         store.Pins
	Projector    PinProjector
	Limits       Limits
	Now          func() time.Time

	Members      MemberStore
	Requests     RequestDedupe
	Forget       MemberForgetter
	Timers       CountTimers
	Reads        store.ReadPositions
	Log          *slog.Logger
	NewRequestID func() string
}

type EditCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	BaseVersion       uint32
	Text              string
	Mentions          *MentionSet
}

type DeleteCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	BaseVersion       uint32
}

type Mutator struct {
	d Deps
}

func New(d Deps) (*Mutator, error) {
	if d.Access == nil || d.Messages == nil || d.Edits == nil || d.Hidden == nil || d.Rooms == nil || d.Events == nil ||
		d.Interactions == nil || d.Counts == nil || d.CountTimers == nil || d.Pins == nil || d.Projector == nil ||
		d.Members == nil || d.Requests == nil || d.Forget == nil || d.Timers == nil || d.Reads == nil {
		return nil, errMissingDeps
	}
	d.Limits = d.Limits.withDefaults()
	if err := d.Limits.validate(); err != nil {
		return nil, err
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.NewRequestID == nil {
		d.NewRequestID = randomRequestID
	}
	return &Mutator{d: d}, nil
}

func (m *Mutator) MemberBatch() int { return m.d.Limits.MemberBatch }

func (m *Mutator) now() time.Time { return m.d.Now().UTC().Truncate(time.Millisecond) }

func (m *Mutator) find(ctx context.Context, key store.MsgKey) (domain.Message, error) {
	found, err := m.d.Messages.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return domain.Message{}, err
	}
	if len(found) == 0 {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	return found[0], nil
}

func (m *Mutator) target(ctx context.Context, action access.Action, tenant, user string, key store.MsgKey) (access.Request, domain.Message, error) {
	req, err := m.d.Access.Admit(ctx, action, tenant, user, key.Room)
	if err != nil {
		return access.Request{}, domain.Message{}, err
	}
	msg, err := m.find(ctx, key)
	if err != nil {
		return access.Request{}, domain.Message{}, err
	}
	req.Author = msg.From
	req.Kind = msg.Kind
	if err := m.d.Access.Allow(ctx, req); err != nil {
		return access.Request{}, domain.Message{}, err
	}
	return req, msg, nil
}

func validKey(key store.MsgKey) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidateThread(key.Thread)
}

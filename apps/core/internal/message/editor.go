package message

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMissingDeps = fmt.Errorf("%w: message editor needs access, messages, edits, hidden, events and interactions", apperr.ErrInvalidArgument)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type Hidden interface {
	Hide(ctx context.Context, user string, key store.MsgKey, at time.Time) (bool, error)
	Get(ctx context.Context, user string, key store.MsgKey) (domain.HiddenMessage, bool, error)
}

type LiveReplies interface {
	CountLiveReplies(ctx context.Context, parent store.MsgKey) (uint32, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access       *access.Checker
	Messages     Messages
	Edits        store.Edits
	Hidden       Hidden
	Events       EventPublisher
	Interactions LiveReplies
	Limits       Limits
	Now          func() time.Time
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

type Editor struct {
	d Deps
}

func New(d Deps) (*Editor, error) {
	if d.Access == nil || d.Messages == nil || d.Edits == nil || d.Hidden == nil || d.Events == nil || d.Interactions == nil {
		return nil, errMissingDeps
	}
	d.Limits = d.Limits.withDefaults()
	if err := d.Limits.validate(); err != nil {
		return nil, err
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Editor{d: d}, nil
}

func (m *Editor) now() time.Time { return m.d.Now().UTC().Truncate(time.Millisecond) }

func (m *Editor) tell(room uint64, ev *chatimv1.Event) {
	_ = m.d.Events.Enqueue(room, []*chatimv1.Event{ev})
}

func (m *Editor) find(ctx context.Context, key store.MsgKey) (domain.Message, error) {
	found, err := m.d.Messages.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return domain.Message{}, err
	}
	if len(found) == 0 {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	return found[0], nil
}

func validKey(key store.MsgKey) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidateThread(key.Thread)
}

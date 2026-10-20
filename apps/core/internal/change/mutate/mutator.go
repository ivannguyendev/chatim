package mutate

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

var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, events and interactions", apperr.ErrInvalidArgument)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access       *access.Checker
	Messages     Messages
	Edits        store.Edits
	Hidden       store.Hidden
	Events       EventPublisher
	Interactions store.Interactions
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

type Mutator struct {
	d Deps
}

func New(d Deps) (*Mutator, error) {
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
	return &Mutator{d: d}, nil
}

func (m *Mutator) now() time.Time { return m.d.Now().UTC().Truncate(time.Millisecond) }

func (m *Mutator) tell(room uint64, ev *chatimv1.Event) {
	_ = m.d.Events.Enqueue(room, []*chatimv1.Event{ev})
}

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

func validKey(key store.MsgKey) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidateThread(key.Thread)
}

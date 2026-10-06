package mutate

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, rooms and events", apperr.ErrInvalidArgument)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	Last(ctx context.Context, room, thread uint64) (uint64, error)
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access   *access.Checker
	Messages Messages
	Edits    store.Edits
	Hidden   store.Hidden
	Rooms    HistoryClearer
	Events   EventPublisher
	Now      func() time.Time
}

type EditCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	BaseVersion       uint32
	Text              string
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
	if d.Access == nil || d.Messages == nil || d.Edits == nil || d.Hidden == nil || d.Rooms == nil || d.Events == nil {
		return nil, errMissingDeps
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Mutator{d: d}, nil
}

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

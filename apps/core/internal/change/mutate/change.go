package mutate

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type change struct {
	action       access.Action
	tenant, user string
	key          store.MsgKey
	base         uint32
	kind         domain.EditKind
	text         string
}

func (m *Mutator) Edit(ctx context.Context, c EditCmd) (domain.Message, error) {
	if err := domain.ValidateText(c.Text); err != nil {
		return domain.Message{}, err
	}
	return m.apply(ctx, change{
		action: access.EditMessage, tenant: c.Tenant, user: c.User,
		key: store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}, base: c.BaseVersion, kind: domain.EditText, text: c.Text,
	})
}

func (m *Mutator) Delete(ctx context.Context, c DeleteCmd) (domain.Message, error) {
	return m.apply(ctx, change{
		action: access.DeleteMessage, tenant: c.Tenant, user: c.User,
		key: store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}, base: c.BaseVersion, kind: domain.EditDelete,
	})
}

func (m *Mutator) apply(ctx context.Context, c change) (domain.Message, error) {
	if err := validKey(c.key); err != nil {
		return domain.Message{}, err
	}
	grant, msg, err := m.target(ctx, c.action, c.tenant, c.user, c.key)
	if err != nil {
		return domain.Message{}, err
	}
	fact, err := m.commit(ctx, c, msg)
	if err != nil {
		return domain.Message{}, err
	}
	if err := m.project(ctx, fact); err != nil {
		return domain.Message{}, err
	}
	snap, err := m.find(ctx, c.key)
	if err != nil {
		return domain.Message{}, err
	}
	_ = m.d.Events.Enqueue(c.key.Room, []*chatimv1.Event{pbconv.MessageChanged(grant.Room.Type, snap, fact)})
	return snap, nil
}

func (m *Mutator) commit(ctx context.Context, c change, msg domain.Message) (domain.Edit, error) {
	if c.base >= math.MaxInt32 {
		return domain.Edit{}, domain.ErrVersionConflict
	}
	next := c.base + 1
	latest, found, err := m.d.Edits.Latest(ctx, c.key)
	if err != nil {
		return domain.Edit{}, err
	}
	switch {
	case found && latest.Version == next && c.matches(latest):
		return latest, nil
	case msg.Deleted || (found && latest.Kind == domain.EditDelete):
		return domain.Edit{}, domain.ErrMessageDeleted
	case c.base != max(msg.Version, latest.Version):
		return domain.Edit{}, domain.ErrVersionConflict
	}
	if err := m.refuseReplied(ctx, c); err != nil {
		return domain.Edit{}, err
	}
	if err := m.keepOriginal(ctx, c, msg, next); err != nil {
		return domain.Edit{}, err
	}
	fact := c.fact(next, m.now())
	switch err := m.d.Edits.Append(ctx, fact); {
	case errors.Is(err, store.ErrEditExists):
		return m.recognize(ctx, c, next)
	case err != nil:
		return domain.Edit{}, err
	}
	return fact, nil
}

func (m *Mutator) refuseReplied(ctx context.Context, c change) error {
	if c.kind != domain.EditDelete {
		return nil
	}
	live, err := m.d.Interactions.CountLiveReplies(ctx, c.key)
	if err != nil {
		return err
	}
	if live > 0 {
		return domain.ErrHasReplies
	}
	return nil
}

func (m *Mutator) keepOriginal(ctx context.Context, c change, msg domain.Message, next uint32) error {
	if next != 1 || c.kind != domain.EditText {
		return nil
	}
	row := domain.Edit{
		Room: c.key.Room, Thread: c.key.Thread, Seq: c.key.Seq, Version: 0, Kind: domain.EditOriginal,
		Tenant: c.tenant, By: msg.From, Text: msg.Text, At: msg.CreatedAt,
	}
	if err := m.d.Edits.Append(ctx, row); err != nil && !errors.Is(err, store.ErrEditExists) {
		return err
	}
	return nil
}

func (m *Mutator) recognize(ctx context.Context, c change, version uint32) (domain.Edit, error) {
	got, err := m.d.Edits.At(ctx, c.key, version)
	if err != nil {
		return domain.Edit{}, err
	}
	if !c.matches(got) {
		return domain.Edit{}, domain.ErrVersionConflict
	}
	return got, nil
}

func (m *Mutator) project(ctx context.Context, f domain.Edit) error {
	if err := m.d.Messages.ApplyEdit(ctx, f); err != nil {
		return err
	}
	if f.Kind != domain.EditDelete {
		return nil
	}
	return m.d.Edits.PurgeText(ctx, store.MsgKey{Room: f.Room, Thread: f.Thread, Seq: f.Seq}, f.Version-1)
}

func (c change) matches(e domain.Edit) bool {
	return e.By == c.user && e.Kind == c.kind && e.Text == c.text
}

func (c change) fact(version uint32, at time.Time) domain.Edit {
	return domain.Edit{
		Room: c.key.Room, Thread: c.key.Thread, Seq: c.key.Seq, Version: version, Kind: c.kind,
		Tenant: c.tenant, By: c.user, Text: c.text, At: at,
	}
}

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
	mentions     domain.Mentions
	keepMentions bool
}

func (m *Mutator) Edit(ctx context.Context, c EditCmd) (domain.Message, error) {
	if err := domain.ValidateText(c.Text); err != nil {
		return domain.Message{}, err
	}
	ch := change{
		action: access.EditMessage, tenant: c.Tenant, user: c.User,
		key: store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}, base: c.BaseVersion, kind: domain.EditText, text: c.Text,
	}
	ch, err := m.withMentions(ch, c.Mentions)
	if err != nil {
		return domain.Message{}, err
	}
	return m.apply(ctx, ch)
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
	grant, msg, err := m.d.Access.Target(ctx, m.d.Messages, c.action, c.tenant, c.user, c.key)
	if err != nil {
		return domain.Message{}, err
	}
	if err := m.resolveMentions(ctx, c, grant); err != nil {
		return domain.Message{}, err
	}
	done, err := m.commit(ctx, c, msg)
	if err != nil {
		return domain.Message{}, err
	}
	if err := m.project(ctx, done.fact); err != nil {
		return domain.Message{}, err
	}
	snap, err := m.find(ctx, c.key)
	if err != nil {
		return domain.Message{}, err
	}
	previous, err := m.previousOf(ctx, done, snap)
	switch {
	case errors.Is(err, store.ErrPreviousPurged):
		return snap, nil
	case err != nil:
		return domain.Message{}, err
	}
	_ = m.d.Events.Enqueue(c.key.Room, []*chatimv1.Event{pbconv.MessageChanged(grant.Room.Type, snap, done.fact, previous)})
	return snap, nil
}

func (m *Mutator) commit(ctx context.Context, c change, msg domain.Message) (committed, error) {
	if c.base >= math.MaxInt32 {
		return committed{}, domain.ErrVersionConflict
	}
	next := c.base + 1
	latest, found, err := m.d.Edits.Latest(ctx, c.key)
	if err != nil {
		return committed{}, err
	}
	switch {
	case found && latest.Version == next && c.matches(latest):
		return committed{fact: latest, before: msg.MentionSet(), known: msg.Version == c.base}, nil
	case msg.Deleted || (found && latest.Kind == domain.EditDelete):
		return committed{}, domain.ErrMessageDeleted
	case c.base != max(msg.Version, latest.Version):
		return committed{}, domain.ErrVersionConflict
	}
	if err := m.refuseReplied(ctx, c); err != nil {
		return committed{}, err
	}
	if err := m.keepOriginal(ctx, c, msg, next); err != nil {
		return committed{}, err
	}
	at := m.now()
	before := previousMentions(msg, latest, found)
	c = c.stamped(before, at)
	fact := c.fact(next, at)
	switch err := m.d.Edits.Append(ctx, fact); {
	case errors.Is(err, store.ErrEditExists):
		got, err := m.recognize(ctx, c, next)
		return committed{fact: got, before: before, known: true}, err
	case err != nil:
		return committed{}, err
	}
	return committed{fact: fact, before: before, known: true}, nil
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
		Mentions: msg.Mentions, MentionAll: msg.MentionAll, MentionAllAt: msg.MentionAllAt,
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

func (c change) fact(version uint32, at time.Time) domain.Edit {
	return domain.Edit{
		Room: c.key.Room, Thread: c.key.Thread, Seq: c.key.Seq, Version: version, Kind: c.kind,
		Tenant: c.tenant, By: c.user, Text: c.text, At: at,
		Mentions: c.mentions.Targets, MentionAll: c.mentions.All, MentionAllAt: c.mentions.AllAt,
	}
}

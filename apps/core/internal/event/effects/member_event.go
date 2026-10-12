package effects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MemberEventName = "member_event"

var (
	errNoMember   = fmt.Errorf("member %w", apperr.ErrNotFound)
	errNotCleared = fmt.Errorf("history clear %w", apperr.ErrNotFound)
)

type MemberEventDeps struct {
	Members MemberLookup
	Rooms   RoomReader
	JS      publish.JetStream
	Now     func() time.Time
}

type memberEffect struct {
	eventPublisher
	members MemberLookup
	rooms   RoomReader
	now     func() time.Time
	delay   time.Duration
}

func (e *memberEffect) setup(name string, deps MemberEventDeps, cfg MessageChangedConfig) error {
	if deps.Members == nil || deps.Rooms == nil || deps.JS == nil {
		return fmt.Errorf("%w: %s needs members, rooms and a jetstream client", apperr.ErrInvalidArgument, name)
	}
	cfg, err := eventConfig(name, cfg)
	if err != nil {
		return err
	}
	e.members, e.rooms, e.now, e.delay = deps.Members, deps.Rooms, deps.Now, cfg.Delay
	if e.now == nil {
		e.now = time.Now
	}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return nil
}

func (e *memberEffect) current(ctx context.Context, room uint64, user string) (domain.Member, error) {
	found, err := e.members.MembersOf(ctx, room, []string{user})
	switch {
	case err != nil:
		return domain.Member{}, err
	case len(found) == 0:
		return domain.Member{}, errNoMember
	}
	return found[0], nil
}

func (e *memberEffect) stamp() time.Time { return e.now().UTC().Truncate(time.Millisecond) }

func memberGone(err error) bool {
	return gone(err) || errors.Is(err, errNoMember) || errors.Is(err, errNotCleared)
}

type MemberEvent struct{ memberEffect }

func NewMemberEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*MemberEvent, error) {
	e := &MemberEvent{}
	if err := e.setup(MemberEventName, deps, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *MemberEvent) Effect() Effect {
	return Effect{Name: MemberEventName, Delay: e.delay, Run: e.run}
}

func (e *MemberEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, memberGone)
}

func (e *MemberEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	doc, err := e.current(ctx, r.Room, r.User)
	switch {
	case err != nil:
		return nil, err
	case doc.Ver > r.Version:
		return nil, nil
	case doc.Ver < r.Version:
		return nil, store.ErrStaleRead
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.MemberEvent(typ, doc), nil
}

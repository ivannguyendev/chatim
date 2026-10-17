package resync

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	roomPage    = 500
	messagePage = store.MaxPageLimit
)

type Rooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	ActiveRooms(ctx context.Context, q store.ActiveQuery) ([]domain.Room, error)
}

type Pages interface {
	Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error)
}

type States interface {
	Mark(ctx context.Context, c store.StateCollection, targets []store.StateTarget) error
}

type Deps struct {
	Rooms        Rooms
	Pages        Pages
	Edits        Edits
	Interactions Interactions
	Pins         Pins
	Members      Members
	Hidden       Hidden
	States       States
	Timers       CountTimers
}

type scanner struct {
	deps   Deps
	opts   Options
	tick   *time.Ticker
	rep    Report
	batch  []mark
	checks []countCheck
	seen   map[countCheck]struct{}
}

func Run(ctx context.Context, deps Deps, opts Options) (Report, error) {
	if err := opts.Validate(); err != nil {
		return Report{DryRun: opts.DryRun}, err
	}
	s := &scanner{deps: deps, opts: opts, tick: time.NewTicker(time.Second / time.Duration(opts.Rate)), rep: Report{DryRun: opts.DryRun}, seen: map[countCheck]struct{}{}}
	defer s.tick.Stop()
	err := s.rooms(ctx)
	return s.rep, err
}

func (s *scanner) rooms(ctx context.Context) error {
	if s.opts.Room != 0 {
		r, err := s.deps.Rooms.Get(ctx, s.opts.Room)
		if err != nil {
			return fmt.Errorf("room %d: %w", s.opts.Room, err)
		}
		if s.opts.Tenant != "" && r.Tenant != s.opts.Tenant {
			return fmt.Errorf("%w: room %d belongs to another tenant", ErrUsage, r.ID)
		}
		return s.room(ctx, r)
	}
	q := store.ActiveQuery{From: s.opts.From, To: s.opts.To, Tenant: s.opts.Tenant, Limit: roomPage}
	for {
		page, err := s.deps.Rooms.ActiveRooms(ctx, q)
		if err != nil {
			return fmt.Errorf("active rooms after %d: %w", q.After, err)
		}
		for _, r := range page {
			if err := s.room(ctx, r); err != nil {
				return err
			}
		}
		if len(page) < roomPage {
			return nil
		}
		q.After = page[len(page)-1].ID
	}
}

func (s *scanner) room(ctx context.Context, r domain.Room) error {
	s.rep.Rooms++
	for _, m := range s.roomMarks(r) {
		if err := s.mark(ctx, m); err != nil {
			return err
		}
	}
	for _, scan := range []func(context.Context, uint64) error{s.timeline, s.edits, s.interactions, s.pins, s.members, s.hidden} {
		if err := scan(ctx, r.ID); err != nil {
			return err
		}
	}
	if err := s.flush(ctx); err != nil {
		return err
	}
	return s.countChecks(ctx)
}

func (s *scanner) roomMarks(r domain.Room) []mark {
	var out []mark
	if s.inRange(r.CreatedAt) {
		out = append(out, mark{store.StateTarget{Collection: store.StateRooms, Bit: store.BitRoomCreated, Room: r.ID}, &s.rep.RoomsCreated})
	}
	if r.MemberCountVer >= 1 {
		out = append(out, mark{store.StateTarget{Collection: store.StateRooms, Bit: store.BitMemberCount, Room: r.ID, Ver: r.MemberCountVer}, &s.rep.MemberCounts})
	}
	if r.InfoVer >= 2 && s.inRange(r.UpdatedAt) {
		out = append(out, mark{store.StateTarget{Collection: store.StateRooms, Bit: store.BitRoomInfo, Room: r.ID, Ver: r.InfoVer}, &s.rep.RoomInfos})
	}
	return out
}

func (s *scanner) timeline(ctx context.Context, room uint64) error {
	q := store.PageQuery{Room: room, Anchor: store.Latest, Limit: messagePage}
	for {
		page, err := s.deps.Pages.Page(ctx, q)
		if err != nil {
			return fmt.Errorf("page room %d before seq %d: %w", room, q.Seq, err)
		}
		for _, m := range slices.Backward(page) {
			if m.CreatedAt.Before(s.opts.From) {
				return nil
			}
			if m.CreatedAt.After(s.opts.To) {
				continue
			}
			t := store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: m.Room, Thread: m.Thread, Seq: m.Seq}
			if err := s.mark(ctx, mark{t, &s.rep.Messages}); err != nil {
				return err
			}
		}
		if len(page) < messagePage {
			return nil
		}
		q.Anchor, q.Seq = store.Before, page[0].Seq
	}
}

func (s *scanner) inRange(t time.Time) bool { return !t.Before(s.opts.From) && !t.After(s.opts.To) }

func (s *scanner) pace(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.tick.C:
		return nil
	}
}

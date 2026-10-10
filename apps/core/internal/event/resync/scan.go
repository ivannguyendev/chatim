package resync

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
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

type Publisher interface {
	PublishMsg(ctx context.Context, m *nats.Msg, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

type Deps struct {
	Rooms        Rooms
	Pages        Pages
	Edits        Edits
	Interactions Interactions
	Pins         Pins
	Members      Members
	Hidden       Hidden
	Pub          Publisher
}

type Target struct {
	SubjectRoot string
	Partitions  int
}

type scanner struct {
	deps   Deps
	target Target
	opts   Options
	tick   *time.Ticker
	rep    Report
	checks []countCheck
	seen   map[countCheck]struct{}
}

func Run(ctx context.Context, deps Deps, target Target, opts Options) (Report, error) {
	if err := opts.Validate(); err != nil {
		return Report{DryRun: opts.DryRun}, err
	}
	s := &scanner{deps: deps, target: target, opts: opts, tick: time.NewTicker(time.Second / time.Duration(opts.Rate)), rep: Report{DryRun: opts.DryRun}, seen: map[countCheck]struct{}{}}
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
	if s.inRange(r.CreatedAt) {
		if err := s.emitCounted(ctx, work.Record{Kind: store.RoomInserted, Room: r.ID, CommittedAt: r.CreatedAt}, &s.rep.RoomRecords); err != nil {
			return err
		}
	}
	for _, scan := range []func(context.Context, uint64) error{s.timeline, s.edits, s.interactions, s.pins, s.members, s.hidden, s.countChecks} {
		if err := scan(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
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
			if err := s.emitCounted(ctx, messageRecord(m), &s.rep.MessageRecords); err != nil {
				return err
			}
		}
		if len(page) < messagePage {
			return nil
		}
		q.Anchor, q.Seq = store.Before, page[0].Seq
	}
}

func messageRecord(m domain.Message) work.Record {
	flags := uint32(store.ReplyMentionFlagsOf(m))
	return work.Record{Kind: store.MessageInserted, Room: m.Room, Thread: m.Thread, Seq: m.Seq, Version: flags, CommittedAt: m.CreatedAt}
}

func (s *scanner) inRange(t time.Time) bool { return !t.Before(s.opts.From) && !t.After(s.opts.To) }

func (s *scanner) emitCounted(ctx context.Context, rec work.Record, counted *int) error {
	if err := s.emit(ctx, rec); err != nil {
		return err
	}
	*counted++
	return nil
}

func (s *scanner) emit(ctx context.Context, rec work.Record) error {
	if s.opts.DryRun {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.tick.C:
	}
	if _, err := s.deps.Pub.PublishMsg(ctx, work.Message(s.target.SubjectRoot, s.target.Partitions, rec)); err != nil {
		return fmt.Errorf("publish work record %s: %w", rec.ID(), err)
	}
	return nil
}

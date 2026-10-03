package recovery

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type verdict uint8

const (
	unchecked verdict = iota
	caughtUp
	inFlight
	republished
	failed
)

type summary struct {
	slots       int
	rooms       int
	republished int
	inFlight    int
	removed     int
	failed      int
	err         error
}

func (s *summary) add(o summary) {
	s.slots += o.slots
	s.rooms += o.rooms
	s.republished += o.republished
	s.inFlight += o.inFlight
	s.removed += o.removed
	s.failed += o.failed
	s.err = cmp.Or(s.err, o.err)
}

func (s *summary) fail(err error) {
	s.failed++
	s.err = cmp.Or(s.err, err)
}

func (s summary) report(ctx context.Context, log *slog.Logger) {
	switch {
	case s.slots == 0 || ctx.Err() != nil:
	case s.failed > 0:
		log.WarnContext(ctx, "recovery pass left rooms for the next pass", "slots", s.slots, "rooms", s.rooms, "republished", s.republished, "in_flight", s.inFlight, "removed", s.removed, "failed", s.failed, "err", s.err)
	default:
		log.DebugContext(ctx, "recovery pass finished", "slots", s.slots, "rooms", s.rooms, "republished", s.republished, "in_flight", s.inFlight, "removed", s.removed)
	}
}

func (s *Sweeper) sweepSlot(ctx context.Context, slot uint16) summary {
	sum := summary{slots: 1}
	readAt := s.now()
	offset := s.cursor[slot]
	rooms, read, err := s.readActive(ctx, slot, offset)
	if err != nil {
		sum.fail(err)
		return sum
	}
	sum.rooms = len(rooms)
	verdicts, errs := s.checkAll(ctx, rooms)
	var stale []activeRoom
	for i, v := range verdicts {
		switch v {
		case caughtUp:
			if s.expired(rooms[i].score, readAt) {
				stale = append(stale, rooms[i])
			}
		case republished:
			sum.republished++
		case inFlight:
			sum.inFlight++
		case failed:
			sum.fail(errs[i])
		case unchecked:
		}
	}
	removed, err := s.removeStale(ctx, slot, stale)
	sum.removed = removed
	if err != nil {
		sum.fail(err)
	}
	s.advance(slot, offset, read, removed)
	return sum
}

func (s *Sweeper) expired(score float64, readAt time.Time) bool {
	return !time.UnixMilli(int64(score)).Add(s.cfg.RemoveAfter).After(readAt)
}

func (s *Sweeper) advance(slot uint16, offset int64, read, removed int) {
	if read < s.cfg.Batch {
		delete(s.cursor, slot)
		return
	}
	s.cursor[slot] = offset + int64(read-removed)
}

func (s *Sweeper) checkAll(ctx context.Context, rooms []activeRoom) ([]verdict, []error) {
	verdicts := make([]verdict, len(rooms))
	errs := make([]error, len(rooms))
	sem := make(chan struct{}, s.cfg.Workers)
	var wg sync.WaitGroup
	for i, r := range rooms {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return verdicts, errs
		}
		wg.Go(func() {
			defer func() { <-sem }()
			verdicts[i], errs[i] = s.check(ctx, r)
		})
	}
	wg.Wait()
	return verdicts, errs
}

func (s *Sweeper) check(ctx context.Context, r activeRoom) (verdict, error) {
	cctx, cancel := context.WithTimeout(ctx, s.cfg.RoomTimeout)
	defer cancel()
	_, last, err := s.deps.Msgs.Last(cctx, r.id, 0)
	if err != nil {
		return failed, fmt.Errorf("last pts of room %d: %w", r.id, err)
	}
	if r.wm >= last {
		return caughtUp, nil
	}
	next, err := s.deps.Msgs.Page(cctx, store.PageQuery{Room: r.id, Anchor: store.After, Seq: r.wm, Limit: 1})
	if err != nil {
		return failed, fmt.Errorf("message after pts %d of room %d: %w", r.wm, r.id, err)
	}
	if len(next) > 0 && s.now().Sub(next[0].CreatedAt) < s.cfg.StaleAfter {
		return inFlight, nil
	}
	if err := s.deps.Rooms.Recover(cctx, r.id, r.wm); err != nil {
		return failed, fmt.Errorf("recover room %d after pts %d: %w", r.id, r.wm, err)
	}
	return republished, nil
}

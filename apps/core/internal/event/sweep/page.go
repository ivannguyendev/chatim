package sweep

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	rebuildFailedMsg = "pending event rebuild failed; the flag stays for the next pass"
	dropMsg          = "dropping pending state of a document whose event cannot be built"
)

var (
	errNoEvent  = errors.New("rebuilder built no event")
	errNoTarget = errors.New("rebuilt event has no state target")
)

type outgoing struct {
	doc    int
	target store.StateTarget
	msg    *nats.Msg
}

type clearing struct {
	doc    int
	target store.StateTarget
}

type pageRun struct {
	coll   store.StateCollection
	failed []bool
	out    []outgoing
	clears []clearing
}

func (s *Sweeper) page(ctx context.Context, c store.StateCollection, docs []store.PendingDoc) (time.Time, error) {
	if len(docs) == 0 {
		return time.Time{}, nil
	}
	p := &pageRun{coll: c, failed: make([]bool, len(docs))}
	rb := s.deps.Rebuilders[c]
	for i, d := range docs {
		s.warnForeign(ctx, d)
		for _, bit := range setBits(d.State & c.Bits()) {
			evs, err := rb.Rebuild(ctx, d, bit)
			s.collect(ctx, p, i, d, bit, evs, err)
		}
	}
	if err := s.pace.take(ctx, s.stop, len(p.out)); err != nil {
		return time.Time{}, err
	}
	s.publish(ctx, p)
	s.clear(ctx, p)
	var failedAt time.Time
	for i, failed := range p.failed {
		if failed {
			failedAt = earliest(failedAt, docs[i].StateAt)
		}
	}
	return failedAt, nil
}

func (s *Sweeper) collect(ctx context.Context, p *pageRun, i int, d store.PendingDoc, bit store.StateBit, evs []*chatimv1.Event, err error) {
	switch {
	case eventkit.Gone(err):
		if target, known := dropTarget(d, bit, err); known {
			s.drop(ctx, p, i, target, err)
		} else {
			s.fail(ctx, p, i, rebuildFailedMsg, errors.Join(err, errUnknownVersion))
		}
	case err != nil:
		s.fail(ctx, p, i, rebuildFailedMsg, err)
	case len(evs) == 0:
		s.fail(ctx, p, i, rebuildFailedMsg, errNoEvent)
	default:
		for _, ev := range evs {
			s.enqueue(ctx, p, i, d.Room, ev)
		}
	}
}

func (s *Sweeper) enqueue(ctx context.Context, p *pageRun, i int, room uint64, ev *chatimv1.Event) {
	targets := publish.StateTargetsOf(ev)
	if len(targets) != 1 || targets[0].Collection != p.coll {
		s.fail(ctx, p, i, rebuildFailedMsg, errNoTarget)
		return
	}
	target := targets[0]
	msg, err := publish.Message(s.cfg.SubjectRoot, room, ev)
	if err != nil {
		s.drop(ctx, p, i, target, err)
		return
	}
	p.out = append(p.out, outgoing{doc: i, target: target, msg: msg})
}

func (s *Sweeper) drop(ctx context.Context, p *pageRun, i int, target store.StateTarget, err error) {
	if verr := target.Validate(); verr != nil {
		s.fail(ctx, p, i, dropMsg, errors.Join(err, verr))
		return
	}
	s.stats.dropped[p.coll].Add(1)
	s.drops.Warn(ctx, dropMsg, "collection", p.coll.String(), "room", target.Room, "bit", uint32(target.Bit), "err", err)
	p.clears = append(p.clears, clearing{doc: i, target: target})
}

func (s *Sweeper) fail(ctx context.Context, p *pageRun, i int, msg string, err error) {
	p.failed[i] = true
	s.stats.failures.Add(1)
	s.fails.Warn(ctx, msg, "collection", p.coll.String(), "err", err)
}

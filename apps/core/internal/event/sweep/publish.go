package sweep

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	publishFailedMsg = "pending event republish failed; the flag stays for the next pass"
	clearFailedMsg   = "pending state clear after republish failed; the next pass republishes"
)

type sent struct {
	out    int
	future jetstream.PubAckFuture
}

func (s *Sweeper) publish(ctx context.Context, p *pageRun) {
	pending := make([]sent, 0, len(p.out))
	for j, o := range p.out {
		f, err := s.deps.JS.PublishMsgAsync(o.msg)
		if err != nil {
			s.publishFailed(ctx, p, o.doc, err)
			continue
		}
		pending = append(pending, sent{out: j, future: f})
	}
	if len(pending) == 0 {
		return
	}
	acks, cancel := context.WithTimeout(ctx, s.cfg.AckTimeout)
	defer cancel()
	for _, x := range pending {
		o := p.out[x.out]
		select {
		case ack := <-x.future.Ok():
			if ack != nil && !ack.Duplicate {
				s.stats.republished[p.coll].Add(1)
			}
			p.clears = append(p.clears, clearing{doc: o.doc, target: o.target})
		case err := <-x.future.Err():
			s.publishFailed(ctx, p, o.doc, err)
		case <-acks.Done():
			s.publishFailed(ctx, p, o.doc, acks.Err())
		}
	}
}

func (s *Sweeper) publishFailed(ctx context.Context, p *pageRun, i int, err error) {
	p.failed[i] = true
	s.stats.publishFailures.Add(1)
	s.fails.Warn(ctx, publishFailedMsg, "collection", p.coll.String(), "err", err)
}

func (s *Sweeper) clear(ctx context.Context, p *pageRun) {
	if len(p.clears) == 0 {
		return
	}
	targets := make([]store.StateTarget, len(p.clears))
	for i, c := range p.clears {
		targets[i] = c.target
	}
	if err := s.deps.States.Clear(ctx, targets); err != nil {
		for _, c := range p.clears {
			p.failed[c.doc] = true
		}
		s.stats.failures.Add(1)
		s.fails.Warn(ctx, clearFailedMsg, "collection", p.coll.String(), "targets", len(targets), "err", err)
	}
}

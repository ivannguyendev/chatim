package flush

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type pending struct {
	Group
	at time.Time
}

type batch struct {
	groups []pending
	size   int
}

func (b *batch) add(p pending) {
	b.groups = append(b.groups, p)
	b.size += len(p.Msgs)
}

type signal uint8

const (
	received signal = iota
	drained
	due
)

type shard struct {
	msgs     store.Messages
	queue    chan pending
	window   time.Duration
	maxBatch int
	timeout  time.Duration
	closeAll func()
	timer    *time.Timer
	carry    *pending
}

func (s *shard) run(ctx context.Context) error {
	s.timer = time.NewTimer(s.window)
	s.timer.Stop()
	for ctx.Err() == nil {
		first, ok := s.next(ctx)
		if !ok {
			break
		}
		b, open := s.collect(ctx, first)
		s.flush(ctx, b)
		if !open {
			break
		}
	}
	return s.abort(ctx)
}

func (s *shard) next(ctx context.Context) (pending, bool) {
	if p := s.carry; p != nil {
		s.carry = nil
		return *p, true
	}
	select {
	case p, ok := <-s.queue:
		return p, ok
	case <-ctx.Done():
		return pending{}, false
	}
}

func (s *shard) collect(ctx context.Context, first pending) (batch, bool) {
	var b batch
	b.add(first)
	if b.size >= s.maxBatch {
		return b, true
	}
	s.timer.Reset(time.Until(first.at.Add(s.window)))
	defer s.timer.Stop()
	for b.size < s.maxBatch {
		p, sig := s.poll(ctx)
		switch {
		case sig == drained:
			return b, false
		case sig == due:
			return b, true
		case b.size+len(p.Msgs) > s.maxBatch:
			s.carry = &p
			return b, true
		}
		b.add(p)
	}
	return b, true
}

func (s *shard) poll(ctx context.Context) (pending, signal) {
	select {
	case p, ok := <-s.queue:
		return p, arrival(ok)
	default:
	}
	select {
	case p, ok := <-s.queue:
		return p, arrival(ok)
	case <-s.timer.C:
	case <-ctx.Done():
	}
	return pending{}, due
}

func arrival(ok bool) signal {
	if ok {
		return received
	}
	return drained
}

func (s *shard) flush(ctx context.Context, b batch) {
	if err := ctx.Err(); err != nil {
		for _, p := range b.groups {
			fail(p, err)
		}
		return
	}
	msgs := make([]domain.Message, 0, b.size)
	for _, p := range b.groups {
		msgs = append(msgs, p.Msgs...)
	}
	res := s.insert(ctx, msgs)
	if len(res) != len(msgs) {
		res = unknown(len(msgs), fmt.Errorf("flush: store returned %d results for %d messages", len(res), len(msgs)))
	}
	for _, p := range b.groups {
		n := len(p.Msgs)
		p.Done(res[:n:n])
		res = res[n:]
	}
}

func (s *shard) insert(ctx context.Context, msgs []domain.Message) []store.Result {
	ictx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.msgs.Insert(ictx, msgs)
}

func (s *shard) abort(ctx context.Context) error {
	err := ctx.Err()
	if err == nil {
		return nil
	}
	s.closeAll()
	if p := s.carry; p != nil {
		s.carry = nil
		fail(*p, err)
	}
	for p := range s.queue {
		fail(p, err)
	}
	return err
}

func fail(p pending, err error) {
	p.Done(unknown(len(p.Msgs), fmt.Errorf("flush aborted: %w", err)))
}

func unknown(n int, err error) []store.Result {
	out := make([]store.Result, n)
	for i := range out {
		out[i] = store.Result{Outcome: store.Unknown, Err: err}
	}
	return out
}

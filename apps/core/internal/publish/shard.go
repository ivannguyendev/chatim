package publish

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type item struct {
	room   uint64
	events []*chatimv1.Event
}

type shard struct {
	js    JetStream
	store *watermarkStore
	cfg   Config
	log   *slog.Logger
	queue chan item
	abort <-chan struct{}
	full  atomic.Bool

	rooms     *tracker
	carry     item
	pending   []*attempt
	retrying  []*attempt
	nextRetry time.Time
	wake      *time.Timer
	givingUp  bool
	overflow  bool
}

func (s *shard) run(ctx context.Context) {
	s.wake = time.NewTimer(time.Hour)
	s.wake.Stop()
	defer s.wake.Stop()
	tick := time.NewTicker(s.cfg.FlushEvery)
	defer tick.Stop()
	defer s.sync(context.WithoutCancel(ctx))
	queue := s.queue
	for {
		s.feed()
		if queue == nil && len(s.carry.events) == 0 && len(s.pending) == 0 && len(s.retrying) == 0 {
			return
		}
		in := queue
		if len(s.carry.events) > 0 || len(s.pending) >= s.cfg.MaxPending {
			in = nil
		}
		var okc <-chan *jetstream.PubAck
		var errc <-chan error
		if len(s.pending) > 0 {
			okc, errc = s.pending[0].okc, s.pending[0].errc
		}
		select {
		case it, open := <-in:
			if !open {
				queue = nil
				continue
			}
			s.carry = it
		case <-okc:
			s.settleHead(nil)
		case err := <-errc:
			s.settleHead(err)
		case <-s.armed():
			s.wakeUp(time.Now())
		case <-tick.C:
			s.sync(ctx)
			if s.rooms.sweep(time.Now()) {
				s.overflow = false
			}
		case <-s.abort:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *shard) feed() {
	for len(s.carry.events) > 0 && len(s.pending) < s.cfg.MaxPending {
		ev := s.carry.events[0]
		s.carry.events = s.carry.events[1:]
		s.hand(s.carry.room, ev)
	}
	if len(s.carry.events) == 0 {
		s.carry = item{}
	}
}

func (s *shard) hand(room uint64, ev *chatimv1.Event) {
	msg, err := message(s.cfg.SubjectRoot, room, ev)
	if err != nil {
		s.log.Error("dropping malformed event", "room", room, "err", err)
		return
	}
	a := &attempt{room: room, pts: ev.GetPts(), msg: msg}
	a.tracked = s.rooms.hand(room, a.pts, time.Now())
	if !a.tracked {
		s.overflowed("tracked rooms", room)
	}
	s.send(a)
}

func (s *shard) settleHead(err error) {
	a := s.pending[0]
	s.pending[0] = nil
	s.pending = s.pending[1:]
	if err != nil {
		s.failed(a, err)
		return
	}
	s.givingUp = false
	if a.tracked && s.rooms.acked(a.room, a.pts) {
		s.overflowed("published events above the watermark", a.room)
	}
}

func (s *shard) sync(ctx context.Context) {
	for range syncPasses {
		reqs := s.rooms.due()
		if len(reqs) == 0 {
			return
		}
		for len(reqs) > 0 {
			n := min(len(reqs), syncChunk)
			values, err := s.store.sync(ctx, reqs[:n])
			if err != nil {
				return
			}
			for i, r := range reqs[:n] {
				s.rooms.settle(r.room, values[i])
			}
			reqs = reqs[n:]
		}
	}
}

func (s *shard) overflowed(limit string, room uint64) {
	if s.overflow {
		return
	}
	s.overflow = true
	s.log.Warn("publish watermark tracking full; affected watermarks stall until recovery republishes", "limit", limit, "room", room)
}

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
	cfg   Config
	log   *slog.Logger
	queue chan item
	abort <-chan struct{}
	full  atomic.Bool

	rooms     roomQueues
	ready     []*attempt
	pending   []*attempt
	retrying  []*attempt
	nextRetry time.Time
	wake      *time.Timer
	givingUp  bool
}

func newShard(js JetStream, cfg Config, log *slog.Logger, abort <-chan struct{}) *shard {
	return &shard{js: js, cfg: cfg, log: log, queue: make(chan item, cfg.QueueSize), abort: abort, rooms: newRoomQueues()}
}

func (s *shard) run(ctx context.Context) {
	s.wake = time.NewTimer(time.Hour)
	s.wake.Stop()
	defer s.wake.Stop()
	queue := s.queue
	for {
		s.feed()
		if queue == nil && s.idle() {
			return
		}
		in := queue
		if len(s.ready) > 0 || s.rooms.held >= s.cfg.QueueSize {
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
			if !s.rooms.hold(it) {
				s.begin(it)
			}
		case <-okc:
			s.settleHead(nil)
		case err := <-errc:
			s.settleHead(err)
		case <-s.armed():
			s.wakeUp(time.Now())
		case <-s.abort:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *shard) idle() bool {
	return s.rooms.empty() && len(s.ready) == 0 && len(s.pending) == 0 && len(s.retrying) == 0
}

func (s *shard) begin(it item) {
	for !s.queueBatch(it) {
		next, ok := s.rooms.finish(it.room)
		if !ok {
			return
		}
		it = next
	}
}

func (s *shard) queueBatch(it item) bool {
	b := &batch{room: it.room}
	for _, ev := range it.events {
		msg, err := message(s.cfg.SubjectRoot, it.room, ev)
		if err != nil {
			s.log.Error("dropping malformed event", "room", it.room, "err", err)
			continue
		}
		b.open++
		s.ready = append(s.ready, &attempt{batch: b, msg: msg})
	}
	if b.open == 0 {
		return false
	}
	s.rooms.start(b)
	return true
}

func (s *shard) feed() {
	for len(s.ready) > 0 && len(s.pending) < s.cfg.MaxPending {
		a := s.ready[0]
		s.ready[0] = nil
		s.ready = s.ready[1:]
		s.send(a)
	}
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
	s.done(a)
}

func (s *shard) done(a *attempt) {
	a.batch.open--
	if a.batch.open > 0 {
		return
	}
	if next, ok := s.rooms.finish(a.batch.room); ok {
		s.begin(next)
	}
}

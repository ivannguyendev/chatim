package publish

import (
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errAckTimeout = errors.New("publish ack timed out")
	errMalformed  = errors.New("event needs an id, a pts, a subject-safe tenant and a known payload")
)

type attempt struct {
	room     uint64
	pts      uint64
	msg      *nats.Msg
	tracked  bool
	tries    int
	okc      <-chan *jetstream.PubAck
	errc     <-chan error
	deadline time.Time
	due      time.Time
}

func message(root string, room uint64, ev *chatimv1.Event) (*nats.Msg, error) {
	kind, ok := eventKind(ev)
	if !ok || ev.GetId() == "" || ev.GetPts() == 0 || !validToken(ev.GetTenant()) {
		return nil, errMalformed
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: roomSubject(root, ev.GetTenant(), room, kind), Data: data, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, ev.GetId())
	return m, nil
}

func eventKind(ev *chatimv1.Event) (string, bool) {
	switch ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return msgCreated, true
	default:
		return "", false
	}
}

func (s *shard) send(a *attempt) {
	a.tries++
	f, err := s.js.PublishMsgAsync(a.msg)
	if err != nil {
		s.failed(a, err)
		return
	}
	a.okc, a.errc, a.deadline = f.Ok(), f.Err(), time.Now().Add(s.cfg.AckTimeout)
	s.pending = append(s.pending, a)
}

func (s *shard) failed(a *attempt, err error) {
	if a.tries < s.cfg.Attempts && len(s.retrying) < s.cfg.MaxRetrying {
		a.due = time.Now().Add(backoff.Jitter(s.retryDelay(a.tries)))
		s.retrying = append(s.retrying, a)
		if s.nextRetry.IsZero() || a.due.Before(s.nextRetry) {
			s.nextRetry = a.due
		}
		return
	}
	if a.tracked {
		s.rooms.dropped(a.room)
	}
	if !s.givingUp {
		s.givingUp = true
		s.log.Warn("event publish abandoned; its watermark stalls until recovery republishes it", "room", a.room, "pts", a.pts, "tries", a.tries, "err", err)
	}
}

func (s *shard) retryDelay(tries int) time.Duration {
	d := s.cfg.RetryBackoff
	for range tries - 1 {
		if d >= s.cfg.MaxBackoff/2 {
			return s.cfg.MaxBackoff
		}
		d *= 2
	}
	return d
}

func (s *shard) armed() <-chan time.Time {
	var at time.Time
	if len(s.pending) > 0 {
		at = s.pending[0].deadline
	}
	if len(s.retrying) > 0 && len(s.pending) < s.cfg.MaxPending && (at.IsZero() || s.nextRetry.Before(at)) {
		at = s.nextRetry
	}
	if at.IsZero() {
		return nil
	}
	s.wake.Reset(time.Until(at))
	return s.wake.C
}

func (s *shard) wakeUp(now time.Time) {
	for len(s.pending) > 0 && !now.Before(s.pending[0].deadline) {
		select {
		case <-s.pending[0].okc:
			s.settleHead(nil)
		case err := <-s.pending[0].errc:
			s.settleHead(err)
		default:
			s.settleHead(errAckTimeout)
		}
	}
	if len(s.retrying) == 0 || now.Before(s.nextRetry) {
		return
	}
	room := s.cfg.MaxPending - len(s.pending)
	var due []*attempt
	rest := s.retrying[:0:0]
	s.nextRetry = time.Time{}
	for _, a := range s.retrying {
		if len(due) < room && !now.Before(a.due) {
			due = append(due, a)
			continue
		}
		rest = append(rest, a)
		if s.nextRetry.IsZero() || a.due.Before(s.nextRetry) {
			s.nextRetry = a.due
		}
	}
	s.retrying = rest
	for _, a := range due {
		s.send(a)
	}
}

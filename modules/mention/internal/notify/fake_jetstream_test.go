package notify_test

import (
	"context"
	"errors"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var errAck = errors.New("ack failed")

type fakeFuture struct {
	ok  chan *jetstream.PubAck
	err chan error
	msg *nats.Msg
}

func (f *fakeFuture) Ok() <-chan *jetstream.PubAck { return f.ok }

func (f *fakeFuture) Err() <-chan error { return f.err }

func (f *fakeFuture) Msg() *nats.Msg { return f.msg }

type fakeJetStream struct {
	mu       sync.Mutex
	seen     map[string]bool
	sent     []*nats.Msg
	failAck  map[string]bool
	refuse   error
	hold     bool
	captured jetstream.StreamConfig
}

func (j *fakeJetStream) PublishMsgAsync(m *nats.Msg, _ ...jetstream.PublishOpt) (jetstream.PubAckFuture, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.refuse != nil {
		return nil, j.refuse
	}
	f := &fakeFuture{ok: make(chan *jetstream.PubAck, 1), err: make(chan error, 1), msg: m}
	j.sent = append(j.sent, m)
	id := m.Header.Get(jetstream.MsgIDHeader)
	switch {
	case j.hold:
	case j.failAck[id]:
		f.err <- errAck
	default:
		if j.seen == nil {
			j.seen = map[string]bool{}
		}
		f.ok <- &jetstream.PubAck{Stream: "CHATIM_NOTIFY", Duplicate: j.seen[id]}
		j.seen[id] = true
	}
	return f, nil
}

func (j *fakeJetStream) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.refuse != nil {
		return nil, j.refuse
	}
	j.captured = cfg
	return nil, nil
}

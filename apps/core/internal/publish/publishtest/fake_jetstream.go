package publishtest

import (
	"slices"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Rule func(m *nats.Msg) error

type JetStream struct {
	mu       sync.Mutex
	ids      map[string]bool
	stored   []*nats.Msg
	attempts []*nats.Msg
	nack     Rule
	refuse   Rule
	silent   func(m *nats.Msg) bool
	holding  bool
	held     []*future
	seq      uint64
}

type future struct {
	ok  chan *jetstream.PubAck
	err chan error
	msg *nats.Msg
}

func (f *future) Ok() <-chan *jetstream.PubAck { return f.ok }

func (f *future) Err() <-chan error { return f.err }

func (f *future) Msg() *nats.Msg { return f.msg }

func (j *JetStream) PublishMsgAsync(m *nats.Msg, _ ...jetstream.PublishOpt) (jetstream.PubAckFuture, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.attempts = append(j.attempts, m)
	if j.refuse != nil {
		if err := j.refuse(m); err != nil {
			return nil, err
		}
	}
	f := &future{ok: make(chan *jetstream.PubAck, 1), err: make(chan error, 1), msg: m}
	switch {
	case j.silent != nil && j.silent(m):
	case j.holding:
		j.held = append(j.held, f)
	default:
		j.resolveLocked(f)
	}
	return f, nil
}

func (j *JetStream) resolveLocked(f *future) {
	if j.nack != nil {
		if err := j.nack(f.msg); err != nil {
			f.err <- err
			return
		}
	}
	id := f.msg.Header.Get(jetstream.MsgIDHeader)
	dup := j.ids[id]
	if !dup {
		if j.ids == nil {
			j.ids = map[string]bool{}
		}
		j.ids[id] = true
		j.stored = append(j.stored, f.msg)
		j.seq++
	}
	f.ok <- &jetstream.PubAck{Stream: "FAKE", Sequence: j.seq, Duplicate: dup}
}

func (j *JetStream) NackWhen(r Rule) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.nack = r
}

func (j *JetStream) RefuseWhen(r Rule) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.refuse = r
}

func (j *JetStream) SilenceWhen(fn func(m *nats.Msg) bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.silent = fn
}

func (j *JetStream) Hold() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.holding = true
}

func (j *JetStream) Release() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.holding = false
	for _, f := range j.held {
		j.resolveLocked(f)
	}
	j.held = nil
}

func (j *JetStream) Held() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.held)
}

func (j *JetStream) Attempts() []*nats.Msg {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.attempts)
}

func (j *JetStream) Stored() []*nats.Msg {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.stored)
}

func (j *JetStream) Events() ([]*chatimv1.Event, error) {
	var out []*chatimv1.Event
	for _, m := range j.Stored() {
		ev := &chatimv1.Event{}
		if err := proto.Unmarshal(m.Data, ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

func MsgID(m *nats.Msg) string { return m.Header.Get(jetstream.MsgIDHeader) }

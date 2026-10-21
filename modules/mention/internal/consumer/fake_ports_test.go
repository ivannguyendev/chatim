package consumer_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errAck = errors.New("ack failed")

type countingMentions struct {
	*memstore.Mentions
	mu      sync.Mutex
	applied map[domain.MessageKey]int
}

func (m *countingMentions) Apply(ctx context.Context, p domain.Plan) error {
	m.mu.Lock()
	m.applied[p.Key]++
	m.mu.Unlock()
	return m.Mentions.Apply(ctx, p)
}

func (m *countingMentions) count(key domain.MessageKey) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applied[key]
}

type fakeVersions struct {
	mu      sync.Mutex
	current map[domain.MessageKey]uint32
	err     error
	asked   [][]domain.MessageKey
}

func (v *fakeVersions) MessageVersions(_ context.Context, keys []domain.MessageKey) (map[domain.MessageKey]uint32, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.asked = append(v.asked, slices.Clone(keys))
	if v.err != nil {
		return nil, v.err
	}
	out := make(map[domain.MessageKey]uint32)
	for _, k := range keys {
		if ver, ok := v.current[k]; ok {
			out[k] = ver
		}
	}
	return out, nil
}

func (v *fakeVersions) calls() [][]domain.MessageKey {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.asked)
}

type fakeResolver struct {
	mu       sync.Mutex
	groups   map[string][]string
	err      error
	block    chan struct{}
	delay    time.Duration
	inFlight int
	calls    int
}

func (r *fakeResolver) Resolve(ctx context.Context, _ string, _ uint64, _ domain.RoomType, sender string, targets []domain.Target) (map[string]domain.Target, error) {
	r.mu.Lock()
	r.calls++
	r.inFlight++
	block, err, delay := r.block, r.err, r.delay
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
	}()
	if delay > 0 {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	out := make(map[string]domain.Target)
	for _, t := range targets {
		users := []string{t.ID}
		if t.Kind == domain.TargetGroup {
			users = r.groups[t.ID]
		}
		for _, u := range users {
			if _, taken := out[u]; !taken && u != sender {
				out[u] = t
			}
		}
	}
	return out, nil
}

func (r *fakeResolver) busy() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inFlight
}

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
	sent     []string
	packets  []*chatimv1.WsPublish
	failAck  bool
	failPart uint32
}

func (j *fakeJetStream) PublishMsgAsync(m *nats.Msg, _ ...jetstream.PublishOpt) (jetstream.PubAckFuture, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f := &fakeFuture{ok: make(chan *jetstream.PubAck, 1), err: make(chan error, 1), msg: m}
	id := m.Header.Get(jetstream.MsgIDHeader)
	d := &chatimv1.WsPublish{}
	if err := proto.Unmarshal(m.Data, d); err != nil || d.GetId() != id {
		j.sent = append(j.sent, m.Subject+" undecodable or id "+id+" differs from the packet")
	} else {
		j.sent = append(j.sent, m.Subject+" "+d.GetNotification().GetId()+" "+strings.Join(d.GetTargets().GetUsers(), ","))
		j.packets = append(j.packets, d)
	}
	if j.failAck || (j.failPart != 0 && d.GetPart() == j.failPart) {
		f.err <- errAck
		return f, nil
	}
	if j.seen == nil {
		j.seen = map[string]bool{}
	}
	f.ok <- &jetstream.PubAck{Stream: "CHATIM_WS", Duplicate: j.seen[id]}
	j.seen[id] = true
	return f, nil
}

func (j *fakeJetStream) wsPackets() []*chatimv1.WsPublish {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.packets)
}

func (j *fakeJetStream) failingPart(part uint32) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.failPart = part
}

func (j *fakeJetStream) published() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := slices.Clone(j.sent)
	slices.Sort(out)
	return out
}

func (j *fakeJetStream) failing(on bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.failAck = on
}

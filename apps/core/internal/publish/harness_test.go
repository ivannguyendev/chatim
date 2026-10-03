package publish_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	tenant        = "acme"
	roomA  uint64 = 101
	roomB  uint64 = 202

	queueFullMsg = "publish queue full; dropping events"
	abandonedMsg = "event publish abandoned; reconciliation must republish it"
	malformedMsg = "dropping malformed event"
)

var (
	errNack   = errors.New("nack")
	sentAt    = time.UnixMilli(1_700_000_000_000).UTC()
	fastSetup = publish.Config{
		SubjectRoot: "evt", Shards: 2, QueueSize: 64, MaxPending: 16, MaxRetrying: 64, Attempts: 3,
		RetryBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond, AckTimeout: 200 * time.Millisecond,
	}
)

type rig struct {
	*publish.Publisher
	js   *publishtest.JetStream
	sink *testlog.Sink
	done chan error
	once sync.Once
	err  error
}

func newRig(t *testing.T, cfg publish.Config) *rig {
	t.Helper()
	rg := &rig{js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
	p, err := publish.New(rg.js, cfg, rg.sink.Logger())
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}
	rg.Publisher = p
	return rg
}

func (rg *rig) start(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rg.done = make(chan error, 1)
	go func() { rg.done <- rg.Run(ctx) }()
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = rg.Close(stop)
		cancel()
		_ = rg.wait()
	})
	return rg
}

func (rg *rig) wait() error {
	rg.once.Do(func() { rg.err = <-rg.done })
	return rg.err
}

func started(t *testing.T, cfg publish.Config) *rig {
	t.Helper()
	return newRig(t, cfg).start(t)
}

func events(room uint64, seqs ...uint64) []*chatimv1.Event {
	out := make([]*chatimv1.Event, len(seqs))
	for i, s := range seqs {
		m := domain.Message{Room: room, Seq: s, Pts: s, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c" + strconv.FormatUint(s, 10), CreatedAt: sentAt}
		out[i] = pbconv.MessageCreated(domain.RoomGroup, m)
	}
	return out
}

func (rg *rig) enqueue(t *testing.T, room uint64, seqs ...uint64) {
	t.Helper()
	if err := rg.Enqueue(room, events(room, seqs...)); err != nil {
		t.Fatalf("Enqueue(%d, %v): %v", room, seqs, err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within 5s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func nackIDs(ids ...string) publishtest.Rule {
	return func(m *nats.Msg) error {
		if slices.Contains(ids, publishtest.MsgID(m)) {
			return errNack
		}
		return nil
	}
}

func storedIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Stored() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}

func attemptsOf(js *publishtest.JetStream, id string) int {
	n := 0
	for _, m := range js.Attempts() {
		if publishtest.MsgID(m) == id {
			n++
		}
	}
	return n
}

func attemptIndexes(js *publishtest.JetStream, id string) []int {
	var out []int
	for i, m := range js.Attempts() {
		if publishtest.MsgID(m) == id {
			out = append(out, i)
		}
	}
	return out
}

package publish_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) {
	testlog.SilenceRedis()
	goleak.VerifyTestMain(m)
}

const (
	tenant        = "acme"
	roomA  uint64 = 101
	roomB  uint64 = 202

	queueFullMsg = "publish queue full; dropping events until recovery republishes them"
	abandonedMsg = "event publish abandoned; its watermark stalls until recovery republishes it"
	overflowMsg  = "publish watermark tracking full; affected watermarks stall until recovery republishes"
	degradedMsg  = "publish watermarks degraded; they stall until redis returns"
	malformedMsg = "dropping malformed event"
)

var (
	errNack   = errors.New("nack")
	sentAt    = time.UnixMilli(1_700_000_000_000).UTC()
	fastSetup = publish.Config{
		SubjectRoot: "evt", Shards: 2, QueueSize: 64, MaxPending: 16, MaxRetrying: 64, Attempts: 3,
		RetryBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond, AckTimeout: 200 * time.Millisecond,
		FlushEvery: time.Millisecond, RedisTimeout: time.Second, RedisCooldown: 20 * time.Millisecond, RoomIdle: time.Hour,
	}
)

type rig struct {
	*publish.Publisher
	js   *publishtest.JetStream
	mr   *miniredis.Miniredis
	sink *testlog.Sink
	done chan error
	once sync.Once
	err  error
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	return mr, client(t, mr)
}

func client(t *testing.T, mr *miniredis.Miniredis) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func newRig(t *testing.T, cfg publish.Config, mr *miniredis.Miniredis) *rig {
	t.Helper()
	if mr == nil {
		mr = miniredis.RunT(t)
	}
	rg := &rig{js: &publishtest.JetStream{}, mr: mr, sink: &testlog.Sink{}}
	p, err := publish.New(rg.js, client(t, mr), cfg, rg.sink.Logger())
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
	return newRig(t, cfg, nil).start(t)
}

func events(room uint64, pts ...uint64) []*chatimv1.Event {
	out := make([]*chatimv1.Event, len(pts))
	for i, p := range pts {
		m := domain.Message{Room: room, Seq: p, Pts: p, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c" + strconv.FormatUint(p, 10), CreatedAt: sentAt}
		out[i] = pbconv.MessageCreated(domain.RoomGroup, m)
	}
	return out
}

func span(from, to uint64) []uint64 {
	var out []uint64
	for p := from; p <= to; p++ {
		out = append(out, p)
	}
	return out
}

func (rg *rig) enqueue(t *testing.T, room uint64, pts ...uint64) {
	t.Helper()
	if err := rg.Enqueue(room, events(room, pts...)); err != nil {
		t.Fatalf("Enqueue(%d, %v): %v", room, pts, err)
	}
}

func watermark(mr *miniredis.Miniredis, room uint64) (uint64, bool) {
	v, err := mr.Get(publish.WatermarkKey(room))
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 64)
	return n, err == nil
}

func waitWatermark(t *testing.T, mr *miniredis.Miniredis, room, want uint64) {
	t.Helper()
	eventually(t, "watermark "+strconv.FormatUint(want, 10), func() bool {
		got, ok := watermark(mr, room)
		return ok && got == want
	})
}

func holdsWatermark(t *testing.T, mr *miniredis.Miniredis, room, want uint64) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	if got, ok := watermark(mr, room); !ok || got != want {
		t.Fatalf("watermark of room %d = %d (present %v), want it held at %d", room, got, ok, want)
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

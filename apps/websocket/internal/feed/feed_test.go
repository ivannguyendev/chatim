package feed

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func testConfig() Config {
	return Config{
		EventStream: "CHATIM_EVT", EventRoot: "evt", PublishStream: "CHATIM_WS", PublishRoot: "websocket.publish",
		Replicas: 1, Workers: 2, Queue: 8, ReplayRate: 100, ConsumeBatch: 500, ConsumeBytes: 8 << 20,
		ReplayMaxAge: 2 * time.Minute,
	}
}

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func startedFeed(t *testing.T, h Handler, b *fakeBroker) *Feed {
	t.Helper()
	f, err := New(testConfig(), h, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Start(t.Context(), b); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return f
}

func TestNewNeedsAHandlerAndAReplayMaxAge(t *testing.T) {
	if _, err := New(testConfig(), nil, quietLog()); !errors.Is(err, errNoHandler) {
		t.Fatalf("New(nil) = %v", err)
	}
	cfg := testConfig()
	cfg.ReplayMaxAge = 0
	if _, err := New(cfg, newRecorder(), quietLog()); !errors.Is(err, errNoMaxAge) {
		t.Fatalf("New without a max age = %v", err)
	}
}

func TestOrderedConfigStartsAfterTheLastStoredMessage(t *testing.T) {
	got := OrderedConfig("evt.>", 42)
	want := jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{"evt.>"}, DeliverPolicy: jetstream.DeliverByStartSequencePolicy, OptStartSeq: 42, InactiveThreshold: 30 * time.Second,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedConfig = %+v, want %+v", got, want)
	}
}

func TestStartEnsuresThePublishStreamAndConsumesBothStreamsFromNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		b.lastSeq["CHATIM_EVT"], b.lastSeq["CHATIM_WS"] = 100, 7
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		want := wspublish.StreamConfig("CHATIM_WS", "websocket.publish", 1, wspublish.DefaultMaxAge, wspublish.DefaultDuplicates)
		if len(b.ensured) != 1 || !reflect.DeepEqual(b.ensured[0], want) {
			t.Fatalf("ensured %+v, want %+v", b.ensured, want)
		}
		evt, ws := b.consumer("CHATIM_EVT"), b.consumer("CHATIM_WS")
		if !reflect.DeepEqual(evt.cfg, OrderedConfig("evt.>", 101)) || !reflect.DeepEqual(ws.cfg, OrderedConfig("websocket.publish.>", 8)) {
			t.Fatalf("consumer configs %+v / %+v", evt.cfg, ws.cfg)
		}
		if evt.opts != 2 || ws.opts != 2 {
			t.Fatalf("consume options %d / %d, want batch limits and an error handler", evt.opts, ws.opts)
		}
		evt.handler(eventMsg("evt.acme.message.5.msg_created", 100, time.Now()))
		evt.handler(eventMsg("evt.acme.message.5.msg_created", 101, time.Now()))
		ws.handler(eventMsg("websocket.publish.acme", 8, time.Now()))
		if err := f.Stop(t.Context()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if !slices.Equal(rec.seqs("5"), []uint64{101}) || len(rec.publish) != 1 || rec.publish[0].Seq != 8 || len(rec.jumps) != 0 {
			t.Fatalf("events %v publish %+v", rec.events, rec.publish)
		}
		select {
		case <-evt.ctx.Closed():
		default:
			t.Fatal("event consumer still running after Stop")
		}
		if b.closed.Load() != 1 {
			t.Fatalf("broker closed %d times, want once", b.closed.Load())
		}
		if err := f.Stop(t.Context()); err != nil || b.closed.Load() != 1 {
			t.Fatalf("second Stop = %v, closes %d", err, b.closed.Load())
		}
		if err := f.Start(t.Context(), b); !errors.Is(err, errStartedTwice) {
			t.Fatalf("Start after Stop = %v", err)
		}
	})
}

func TestStartFailureStopsWhatStartedAndClosesTheBroker(t *testing.T) {
	for name, fail := range map[string]func(*fakeBroker){
		"ensure stream":   func(b *fakeBroker) { b.ensureErr = errBoom },
		"second consumer": func(b *fakeBroker) { b.consumerErr["CHATIM_WS"] = errBoom },
		"second consume":  func(b *fakeBroker) { b.consumeErr["CHATIM_WS"] = errBoom },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b := newFakeBroker()
				fail(b)
				f, _ := New(testConfig(), newRecorder(), quietLog())
				if err := f.Start(t.Context(), b); !errors.Is(err, errBoom) {
					t.Fatalf("Start = %v, want boom", err)
				}
				if b.closed.Load() != 1 {
					t.Fatal("broker left open after a failed start")
				}
				if c := b.consumer("CHATIM_EVT"); c != nil && c.ctx != nil {
					select {
					case <-c.ctx.Closed():
					default:
						t.Fatal("event consumer left running after a failed start")
					}
				}
			})
		})
	}
}

func TestStopDrainsQueuedEventsBeforeClosingNATS(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		release := rec.block("5")
		f := startedFeed(t, rec, b)
		evt := b.consumer("CHATIM_EVT")
		for seq := uint64(1); seq <= 6; seq++ {
			evt.handler(eventMsg("evt.acme.message.5.msg_created", seq, time.Now()))
		}
		go func() {
			time.Sleep(500 * time.Millisecond)
			close(release)
		}()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		if err := f.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if got := rec.seqs("5"); !slices.Equal(got, []uint64{1, 2, 3, 4, 5, 6}) {
			t.Fatalf("drained %v, want 1..6", got)
		}
	})
}

func TestStopReleasesAReceiverWaitingOnAFullQueueAndGivesUpOnAStuckHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		release := rec.block("5")
		f := startedFeed(t, rec, b)
		evt := b.consumer("CHATIM_EVT")
		go func() {
			for seq := uint64(1); seq <= 20; seq++ {
				evt.handler(eventMsg("evt.acme.message.5.msg_created", seq, time.Now()))
			}
		}()
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		if err := f.Stop(ctx); !errors.Is(err, errFanoutStuck) {
			t.Fatalf("Stop = %v, want the stuck fanout reported", err)
		}
		if b.closed.Load() != 1 {
			t.Fatal("broker left open after a stuck stop")
		}
		close(release)
		synctest.Wait()
		if got := rec.seqs("5"); !slices.Equal(got, []uint64{1}) {
			t.Fatalf("delivered %v after giving up, want only the event in hand", got)
		}
		if f.dropped.Load() == 0 {
			t.Fatal("events refused at stop were not counted")
		}
	})
}

func TestFeedMetricsFollowTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		f, _ := New(testConfig(), newRecorder(), quietLog())
		read := func(name, stream string) float64 {
			for _, s := range f.Sources() {
				if s.Name == name && s.Labels["stream"] == stream {
					return s.Read()
				}
			}
			t.Fatalf("no metric %s{%s}", name, stream)
			return 0
		}
		if read("nats_connected", "") != 0 || read("nats_reconnects_total", "") != 0 || read("fanout_queue_depth", "") != 0 {
			t.Fatal("metrics before start are not zero")
		}
		if err := f.Start(t.Context(), b); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Stop(t.Context()) }()
		b.reconnects.Store(2)
		if read("nats_connected", "") != 1 || read("nats_reconnects_total", "") != 2 {
			t.Fatal("connection metrics wrong after start")
		}
		b.connected.Store(false)
		if read("nats_connected", "") != 0 || f.Connected() {
			t.Fatal("nats_connected stays 1 after the connection dropped")
		}
		b.consumer("CHATIM_EVT").handler(eventMsg("evt.bad", 1, time.Now()))
		msg := eventMsg("evt.acme.message.5.msg_created", 2, time.Now().Add(-2*time.Second))
		msg.meta.NumPending = 4
		b.consumer("CHATIM_EVT").handler(msg)
		if read("consumer_lag_seconds", "CHATIM_EVT") != 2 || read("consumer_pending", "CHATIM_EVT") != 4 ||
			read("events_unknown_total", "") != 1 || read("consumer_resets_total", "CHATIM_WS") != 0 ||
			read("consumer_stale_skipped_total", "CHATIM_EVT") != 0 || read("fanout_handler_panics_total", "") != 0 ||
			read("events_dropped_at_stop_total", "") != 0 || read("catchup_jumps_total", "CHATIM_EVT") != 0 ||
			read("catchup_jump_failures_total", "CHATIM_WS") != 0 {
			t.Fatal("stream metrics wrong")
		}
	})
}

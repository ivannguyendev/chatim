package feed

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const roomSubject = "evt.acme.message.5.msg_created"

func stopFeed(t *testing.T, f *Feed) {
	t.Helper()
	if err := f.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func readMetric(t *testing.T, f *Feed, name, stream string) float64 {
	t.Helper()
	for _, s := range f.Sources() {
		if s.Name == name && s.Labels["stream"] == stream {
			return s.Read()
		}
	}
	t.Fatalf("no metric %s{%s}", name, stream)
	return 0
}

func TestALagUnderTheMaxAgeDeliversEverythingWithoutAJump(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		evt := b.consumer("CHATIM_EVT")
		evt.handler(eventMsg(roomSubject, 1, time.Now().Add(-2*time.Minute+time.Second)))
		evt.handler(eventMsg(roomSubject, 2, time.Now()))
		stopFeed(t, f)
		jumps, _ := rec.history()
		if !slices.Equal(rec.seqs("5"), []uint64{1, 2}) || len(jumps) != 0 || len(b.consumersOf("CHATIM_EVT")) != 1 {
			t.Fatalf("delivered %v, jumps %v, consumers %d", rec.seqs("5"), jumps, len(b.consumersOf("CHATIM_EVT")))
		}
	})
}

func TestTheAgeIsTheStreamStoreTimeNotTheEventTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		replayed := eventMsg(roomSubject, 1, time.Now())
		raw, err := proto.Marshal(&chatimv1.Event{Id: "e1", Ts: timestamppb.New(time.Now().Add(-time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		replayed.data = raw
		b.consumer("CHATIM_EVT").handler(replayed)
		stopFeed(t, f)
		if jumps, _ := rec.history(); !slices.Equal(rec.seqs("5"), []uint64{1}) || len(jumps) != 0 {
			t.Fatalf("a sweeper replay stored now: delivered %v, jumps %v", rec.seqs("5"), jumps)
		}
	})
}

func TestALagOverTheMaxAgeSkipsToTheStreamEndAfterTheQueuedEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		b.lastSeq["CHATIM_EVT"] = 10
		rec := newRecorder()
		release := rec.block("5")
		f := startedFeed(t, rec, b)
		old := b.consumer("CHATIM_EVT")
		old.handler(eventMsg(roomSubject, 11, time.Now()))
		b.set(func(b *fakeBroker) { b.lastSeq["CHATIM_EVT"] = 500 })
		old.handler(eventMsg(roomSubject, 12, time.Now().Add(-3*time.Minute)))
		synctest.Wait()
		if len(b.consumersOf("CHATIM_EVT")) != 1 {
			t.Fatal("the feed skipped before the queued events were routed")
		}
		close(release)
		synctest.Wait()
		consumers := b.consumersOf("CHATIM_EVT")
		if len(consumers) != 2 || consumers[1].cfg.OptStartSeq != 501 {
			t.Fatalf("consumers %d, want a new one starting at 501", len(consumers))
		}
		select {
		case <-old.ctx.Closed():
		default:
			t.Fatal("the old consumer still runs after the skip")
		}
		old.handler(eventMsg(roomSubject, 13, time.Now()))
		fresh := consumers[1]
		fresh.handler(eventMsg(roomSubject, 500, time.Now()))
		fresh.handler(eventMsg(roomSubject, 501, time.Now()))
		stopFeed(t, f)
		jumps, log := rec.history()
		if !slices.Equal(jumps, []Jump{{Reason: JumpLagTooOld, Seq: 500}}) || !slices.Equal(log, []string{"event 11", "jumped 500", "event 501"}) {
			t.Fatalf("jumps %v, order %v", jumps, log)
		}
		if readMetric(t, f, "catchup_jumps_total", "CHATIM_EVT") != 1 || readMetric(t, f, "catchup_jumps_total", "CHATIM_WS") != 0 {
			t.Fatal("catchup_jumps_total did not count the skip on the event stream only")
		}
	})
}

func TestThePublishStreamSkipsWithoutTellingTheHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		b.set(func(b *fakeBroker) { b.lastSeq["CHATIM_WS"] = 40 })
		b.consumer("CHATIM_WS").handler(eventMsg("websocket.publish.acme", 1, time.Now().Add(-3*time.Minute)))
		synctest.Wait()
		consumers := b.consumersOf("CHATIM_WS")
		if len(consumers) != 2 || consumers[1].cfg.OptStartSeq != 41 {
			t.Fatalf("publish consumers %d, want a new one at 41", len(consumers))
		}
		consumers[1].handler(eventMsg("websocket.publish.acme", 41, time.Now()))
		stopFeed(t, f)
		if jumps, _ := rec.history(); len(jumps) != 0 || len(rec.publish) != 1 || rec.publish[0].Seq != 41 {
			t.Fatalf("jumps %v publish %+v", jumps, rec.publish)
		}
		if readMetric(t, f, "catchup_jumps_total", "CHATIM_WS") != 1 {
			t.Fatal("publish stream skip not counted")
		}
	})
}

func TestARecreatedStreamRestartsAtItsEndAsConsumerLost(t *testing.T) {
	for name, recreate := range map[string]func(*fakeBroker){
		"sequence fell":   func(b *fakeBroker) { b.lastSeq["CHATIM_EVT"] = 2 },
		"created changed": func(b *fakeBroker) { b.created["CHATIM_EVT"] = time.Now() },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b := newFakeBroker()
				b.lastSeq["CHATIM_EVT"] = 90
				rec := newRecorder()
				f := startedFeed(t, rec, b)
				b.consumer("CHATIM_EVT").handler(eventMsg(roomSubject, 91, time.Now()))
				b.set(func(b *fakeBroker) { b.lastSeq["CHATIM_EVT"] = 91 })
				time.Sleep(watchInterval)
				synctest.Wait()
				if len(b.consumersOf("CHATIM_EVT")) != 1 {
					t.Fatal("a healthy stream was restarted")
				}
				b.set(recreate)
				time.Sleep(watchInterval)
				synctest.Wait()
				consumers := b.consumersOf("CHATIM_EVT")
				last := b.consumer("CHATIM_EVT").cfg.OptStartSeq - 1
				if len(consumers) != 2 {
					t.Fatalf("consumers %d after the stream was recreated", len(consumers))
				}
				consumers[1].handler(eventMsg(roomSubject, last+1, time.Now()))
				stopFeed(t, f)
				jumps, _ := rec.history()
				if !slices.Equal(jumps, []Jump{{Reason: JumpConsumerLost, Seq: last}}) || !slices.Equal(rec.seqs("5"), []uint64{91, last + 1}) {
					t.Fatalf("jumps %v delivered %v", jumps, rec.seqs("5"))
				}
			})
		})
	}
}

func TestAFailedSkipIsTriedAgainByTheWatchdogAsConsumerLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		b.set(func(b *fakeBroker) { b.consumerErr["CHATIM_EVT"] = errBoom; b.lastSeq["CHATIM_EVT"] = 7 })
		b.consumer("CHATIM_EVT").handler(eventMsg(roomSubject, 1, time.Now().Add(-3*time.Minute)))
		synctest.Wait()
		if readMetric(t, f, "catchup_jump_failures_total", "CHATIM_EVT") != 1 || len(b.consumersOf("CHATIM_EVT")) != 1 {
			t.Fatal("the failed skip was not counted")
		}
		b.set(func(b *fakeBroker) { delete(b.consumerErr, "CHATIM_EVT") })
		time.Sleep(watchInterval)
		synctest.Wait()
		consumers := b.consumersOf("CHATIM_EVT")
		if len(consumers) != 2 || consumers[1].cfg.OptStartSeq != 8 {
			t.Fatalf("consumers %d after the watchdog pass", len(consumers))
		}
		stopFeed(t, f)
		jumps, _ := rec.history()
		if !slices.Equal(jumps, []Jump{{Reason: JumpLagTooOld, Seq: 7}, {Reason: JumpConsumerLost, Seq: 7}}) {
			t.Fatalf("jumps %v", jumps)
		}
	})
}

func TestAConsumerThatStopsDeliveringWhileBehindIsSkippedAsLagTooOld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		b.set(func(b *fakeBroker) { b.lastSeq["CHATIM_EVT"] = 3 })
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if len(b.consumersOf("CHATIM_EVT")) != 1 {
			t.Fatal("skipped before the consumer was stuck for the max age")
		}
		time.Sleep(watchInterval)
		synctest.Wait()
		stopFeed(t, f)
		if jumps, _ := rec.history(); !slices.Equal(jumps, []Jump{{Reason: JumpLagTooOld, Seq: 3}}) || len(b.consumersOf("CHATIM_EVT")) != 2 {
			t.Fatalf("jumps %v consumers %d", jumps, len(b.consumersOf("CHATIM_EVT")))
		}
	})
}

func TestEnvelopesCarryWhetherTheFeedIsCatchingUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got := make(chan Envelope, 2)
		r, fan := newTestReceiver(t.Context(), handlerFuncs{event: func(e Envelope) { got <- e }}, false, 1000)
		r.handle(0, eventMsg("evt.acme.message.1.msg_created", 1, time.Now().Add(-catchUpAge-time.Millisecond)))
		r.handle(0, eventMsg("evt.acme.message.1.msg_created", 2, time.Now().Add(-catchUpAge)))
		if err := fan.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if first, second := <-got, <-got; !first.CatchingUp || second.CatchingUp {
			t.Fatalf("catching up = %v / %v, want true then false", first.CatchingUp, second.CatchingUp)
		}
	})
}

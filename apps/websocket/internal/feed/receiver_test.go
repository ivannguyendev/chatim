package feed

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func newTestReceiver(ctx context.Context, rec Handler, publish bool, perSecond int) (*receiver, *fanout) {
	fan := newFanout(2, 1024, rec)
	root := "evt"
	if publish {
		root = "websocket.publish"
	}
	return &receiver{ctx: ctx, root: root, publish: publish, fan: fan, limiter: replayLimiter(perSecond), stats: &streamStats{stream: "S"}, unknown: &atomic.Uint64{}}, fan
}

func TestReceiverHandsTheEnvelopeToTheRoomWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got := make(chan Envelope, 1)
		r, fan := newTestReceiver(t.Context(), handlerFuncs{event: func(e Envelope) { got <- e }}, false, 100)
		stored := time.Now()
		r.handle(eventMsg("evt.acme.mention.42.msg_created", 7, stored))
		if err := fan.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		e := <-got
		if e.Tenant != "acme" || e.DataKind != "mention" || e.Room != "42" || e.Kind != "msg_created" ||
			e.Seq != 7 || !e.StoredAt.Equal(stored) || e.ID != "evt.acme.mention.42.msg_created" || len(e.Data) != 1 {
			t.Fatalf("envelope = %+v", e)
		}
	})
}

func TestReceiverHandsPublishPacketsToPublishWithTheirID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got := make(chan Envelope, 2)
		r, fan := newTestReceiver(t.Context(), handlerFuncs{publish: func(e Envelope) { got <- e }}, true, 100)
		withID := eventMsg("websocket.publish.acme", 3, time.Now())
		withID.header.Set("Nats-Msg-Id", "n1-p0-abcd")
		noID := eventMsg("websocket.publish.beta", 4, time.Now())
		noID.header = nil
		r.handle(withID)
		r.handle(noID)
		if err := fan.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		seen := map[string]Envelope{}
		for range 2 {
			e := <-got
			seen[e.Tenant] = e
		}
		if seen["acme"].ID != "n1-p0-abcd" || seen["acme"].Seq != 3 || seen["beta"].ID != "" || seen["beta"].Seq != 4 {
			t.Fatalf("publish envelopes = %+v", seen)
		}
	})
}

func TestReceiverDropsAndCountsMalformedMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		r, fan := newTestReceiver(t.Context(), rec, false, 100)
		r.handle(eventMsg("evt.acme.message.42", 1, time.Now()))
		r.handle(eventMsg("evt.acme..42.msg_created", 2, time.Now()))
		r.handle(&fakeMsg{subject: "evt.acme.message.42.msg_created", metaErr: errors.New("no metadata")})
		if err := fan.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := r.unknown.Load(); got != 3 || len(rec.events) != 0 {
			t.Fatalf("unknown = %d, delivered %v; want 3 dropped", got, rec.events)
		}
	})
}

func TestReceiverLimitsTheRateOnlyWhileCatchingUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		ctx, cancel := context.WithCancel(t.Context())
		r, fan := newTestReceiver(ctx, rec, false, 100)
		for seq := uint64(1); seq <= 200; seq++ {
			r.handle(eventMsg("evt.acme.message.1.msg_created", seq, time.Now().Add(-catchUpAge)))
		}
		synctest.Wait()
		if got := len(rec.seqs("1")); got != 200 {
			t.Fatalf("fresh messages delivered %d, want all 200 without waiting", got)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for seq := uint64(201); seq <= 600; seq++ {
				r.handle(eventMsg("evt.acme.message.2.msg_created", seq, time.Now().Add(-10*time.Second)))
			}
		}()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := len(rec.seqs("2")); got > 10+200+1 || got < 200 {
			t.Fatalf("catch-up delivered %d in 2s, want about 100/s plus a burst of 10", got)
		}
		cancel()
		<-done
		if err := fan.close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := len(rec.seqs("2")); got >= 400 {
			t.Fatalf("delivered %d after stop, want the rest dropped", got)
		}
	})
}

func TestStreamStatsTrackLagPendingAndConsumerResets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &streamStats{stream: "S"}
		msg := eventMsg("evt.a.message.1.msg_created", 1, time.Now().Add(-3*time.Second))
		msg.meta.NumPending = 12
		s.observe(msg.meta, time.Now())
		if s.lagSeconds() != 3 || s.pending.Load() != 12 || s.resets.Load() != 0 {
			t.Fatalf("lag %v pending %d resets %d", s.lagSeconds(), s.pending.Load(), s.resets.Load())
		}
		for _, name := range []string{"Abc123_1", "Abc123_3", "Abc123_3", "Abc123_2", "noserial", "x_y"} {
			msg.meta.Consumer = name
			s.observe(msg.meta, time.Now())
		}
		if s.resets.Load() != 2 {
			t.Fatalf("resets = %d, want 2 (serial 1 to 3)", s.resets.Load())
		}
		msg.meta.Timestamp = time.Now().Add(time.Second)
		s.observe(msg.meta, time.Now())
		if s.lagSeconds() != 0 {
			t.Fatalf("lag = %v for a stored time ahead of the clock, want 0", s.lagSeconds())
		}
	})
}

type handlerFuncs struct {
	event, publish func(Envelope)
}

func (h handlerFuncs) Event(e Envelope) {
	if h.event != nil {
		h.event(e)
	}
}

func (h handlerFuncs) Publish(e Envelope) {
	if h.publish != nil {
		h.publish(e)
	}
}

package session

import (
	"strings"
	"testing"
	"testing/synctest"
)

func offerOptions() Options {
	opts := testOptions()
	opts.SendQueue = 8
	opts.SendQueueBytes = 1024
	return opts
}

func TestOfferAcceptsOnlyBelowHalfOfTheFrameQueueAndNeverCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, offerOptions())
		tc, c := h.dialConnection(false)
		sink := roomSink{c: c}
		ids := []string{"a-", "b-", "c-", "d-"}
		for _, id := range ids {
			if !sink.Offer(sizedFrame(t, id, 20)) {
				t.Fatalf("offer %s refused below half of the frame queue", id)
			}
			synctest.Wait()
		}
		if got := len(c.out); got != 3 {
			t.Fatalf("queued frames = %d, want 3 behind the one being written", got)
		}
		if sink.Offer(sizedFrame(t, "e-", 20)) {
			t.Fatal("an offer reaching half of the frame queue was accepted")
		}
		if c.closing.Load() != 0 {
			t.Fatalf("Offer closed the connection with %d", c.closing.Load())
		}
		if !sink.TrySend(sizedFrame(t, "f-", 20)) || !sink.TrySend(sizedFrame(t, "g-", 20)) {
			t.Fatal("TrySend refused while the queue has room")
		}
		if c.closing.Load() != 0 || len(c.out) != 5 {
			t.Fatalf("closing %d queued %d after TrySend", c.closing.Load(), len(c.out))
		}
		tc.startReading()
		for _, want := range append(ids, "f-", "g-") {
			if got := tc.nextFrame().GetEvent().GetId(); !strings.HasPrefix(got, want) {
				t.Fatalf("frame = %q, want %s", got, want)
			}
		}
		synctest.Wait()
		if !sink.Offer(sizedFrame(t, "h-", 20)) {
			t.Fatal("an offer was refused after the queue drained")
		}
		if got := tc.nextFrame().GetEvent().GetId(); !strings.HasPrefix(got, "h-") {
			t.Fatalf("frame = %q, want h-", got)
		}
		if !tc.isOpen() {
			t.Fatal("connection closed")
		}
		tc.close()
	})
}

func TestOfferAcceptsOnlyBelowHalfOfTheByteBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, func() Options { opts := offerOptions(); opts.SendQueue = 64; return opts }())
		tc, c := h.dialConnection(false)
		sink := roomSink{c: c}
		if !sink.Offer(sizedFrame(t, "a-", 200)) || !sink.Offer(sizedFrame(t, "b-", 200)) {
			t.Fatal("offers below half of the byte budget were refused")
		}
		if sink.Offer(sizedFrame(t, "c-", 200)) {
			t.Fatal("an offer over half of the byte budget was accepted")
		}
		if !sink.Offer(sizedFrame(t, "d-", 100)) {
			t.Fatal("an offer ending below half of the byte budget was refused")
		}
		if sink.Offer(sizedFrame(t, "e-", 12)) {
			t.Fatal("an offer reaching half of the byte budget was accepted")
		}
		if got := h.queuedBytes(); got != 500 {
			t.Fatalf("queued bytes = %v, want 500", got)
		}
		if !sink.TrySend(sizedFrame(t, "f-", 300)) || c.closing.Load() != 0 || h.queuedBytes() != 800 {
			t.Fatalf("closing %d queued bytes %v after TrySend", c.closing.Load(), h.queuedBytes())
		}
		tc.startReading()
		for _, want := range []string{"a-", "b-", "d-", "f-"} {
			if got := tc.nextFrame().GetEvent().GetId(); !strings.HasPrefix(got, want) {
				t.Fatalf("frame = %q, want %s", got, want)
			}
		}
		synctest.Wait()
		if got := h.queuedBytes(); got != 0 || !tc.isOpen() {
			t.Fatalf("queued bytes %v open %v after the writes", got, tc.isOpen())
		}
		tc.close()
	})
}

func TestOfferRefusesAClosingOrClosedConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, offerOptions())
		tc, c := h.dialConnection(false)
		sink := roomSink{c: c}
		c.closeWith(CodeGoingAway)
		if sink.Offer(sizedFrame(t, "a-", 10)) {
			t.Fatal("a closing connection accepted an offer")
		}
		tc.startReading()
		if got := tc.waitClosed(); got != CodeGoingAway {
			t.Fatalf("close code = %d, want %d", got, CodeGoingAway)
		}
		if sink.Offer(sizedFrame(t, "b-", 10)) || h.queuedBytes() != 0 {
			t.Fatalf("a closed connection accepted an offer, queued bytes %v", h.queuedBytes())
		}
	})
}

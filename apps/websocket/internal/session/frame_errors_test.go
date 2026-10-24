package session

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	wireUnsupported = chatimv1.FrameErrorReason_FRAME_ERROR_REASON_UNSUPPORTED
	wireInvalid     = chatimv1.FrameErrorReason_FRAME_ERROR_REASON_INVALID
	wireRate        = chatimv1.FrameErrorReason_FRAME_ERROR_REASON_RATE
)

func (tc *testClient) expectFrameError(want chatimv1.FrameErrorReason) {
	tc.t.Helper()
	f := tc.nextFrame()
	if f.GetFrameError() == nil || f.GetFrameError().GetReason() != want {
		tc.t.Fatalf("frame = %v, want frame_error %v", f, want)
	}
}

func (tc *testClient) drainFrames() []*chatimv1.ServerFrame {
	var out []*chatimv1.ServerFrame
	for {
		select {
		case f := <-tc.frames:
			out = append(out, f)
		default:
			return out
		}
	}
}

func frameErrorsIn(frames []*chatimv1.ServerFrame) []chatimv1.FrameErrorReason {
	var out []chatimv1.FrameErrorReason
	for _, f := range frames {
		if f.GetFrameError() != nil {
			out = append(out, f.GetFrameError().GetReason())
		}
	}
	return out
}

func (h *harness) frameErrors(reason string) float64 {
	return h.sessionMetric("frame_errors_total", "reason", reason)
}

func TestATextFrameGetsFrameErrorUnsupportedAndALaterRequestIsAnswered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClient("u1")
		_ = tc.conn.WriteMessage(gws.OpcodeText, []byte("hello"))
		tc.expectFrameError(wireUnsupported)
		tc.history(1)
		if resp := tc.next(); resp.GetId() != 1 || resp.GetCode() != 0 {
			t.Fatalf("response after the text frame = %v", resp)
		}
		if !tc.isOpen() || h.frameErrors("unsupported") != 1 {
			t.Fatalf("open %v unsupported %v", tc.isOpen(), h.frameErrors("unsupported"))
		}
		tc.close()
	})
}

func TestAnUndecodableFrameGetsFrameErrorInvalidAndTheConnectionStaysOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.write([]byte{0x52, 0x05, 0x01})
		tc.expectFrameError(wireInvalid)
		tc.auth(h.token("u1", time.Hour))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("frame after the bad frame = %v, want ready", f)
		}
		if !tc.isOpen() || h.frameErrors("invalid") != 1 {
			t.Fatalf("open %v invalid %v", tc.isOpen(), h.frameErrors("invalid"))
		}
		tc.close()
	})
}

func TestAFrameFloodGetsAtMostOneFrameErrorPerSecondAndStaysOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		tc := h.readyClient("u1")
		for range 20 {
			tc.heartbeat()
		}
		_ = tc.conn.WriteMessage(gws.OpcodeText, []byte("hello"))
		synctest.Wait()
		if got := frameErrorsIn(tc.drainFrames()); len(got) != 1 || got[0] != wireRate {
			t.Fatalf("frame errors = %v, want one rate", got)
		}
		if h.frameErrors("rate") != 19 || h.frameErrors("unsupported") != 0 {
			t.Fatalf("rate %v unsupported %v", h.frameErrors("rate"), h.frameErrors("unsupported"))
		}
		time.Sleep(time.Second)
		for range 5 {
			tc.heartbeat()
		}
		synctest.Wait()
		if got := frameErrorsIn(tc.drainFrames()); len(got) != 1 || got[0] != wireRate {
			t.Fatalf("frame errors a second later = %v, want one rate", got)
		}
		tc.history(1)
		if resp := tc.next(); resp.GetCode() == 0 {
			t.Fatalf("a request over the rate was answered with %v", resp)
		}
		if !tc.isOpen() {
			t.Fatal("connection closed by the flood")
		}
		tc.close()
	})
}

func TestATextFloodIsBoundedByTheSharedLimiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		tc := h.readyClient("u1")
		for range 10 {
			_ = tc.conn.WriteMessage(gws.OpcodeText, []byte("hello"))
		}
		tc.write([]byte{0x52, 0x05, 0x01})
		synctest.Wait()
		if got := frameErrorsIn(tc.drainFrames()); len(got) != 1 || got[0] != wireUnsupported {
			t.Fatalf("frame errors = %v, want one unsupported", got)
		}
		if h.frameErrors("unsupported") != 2 || h.frameErrors("invalid") != 0 || h.frameErrors("rate") != 9 {
			t.Fatalf("unsupported %v invalid %v rate %v", h.frameErrors("unsupported"), h.frameErrors("invalid"), h.frameErrors("rate"))
		}
		tc.history(1)
		if resp := tc.next(); resp.GetCode() == 0 {
			t.Fatalf("a request after the flood was answered with %v", resp)
		}
		if h.core.calls.Load() != 0 || !tc.isOpen() {
			t.Fatalf("core calls %d open %v", h.core.calls.Load(), tc.isOpen())
		}
		tc.close()
	})
}

func TestAPingFloodOverTheRateGetsNoPongAndNoClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		tc := h.dial(true)
		for range 10 {
			_ = tc.conn.WritePing(nil)
		}
		synctest.Wait()
		if got := len(tc.pongs); got != 3 {
			t.Fatalf("pongs = %d, want 3", got)
		}
		if got := frameErrorsIn(tc.drainFrames()); len(got) != 1 || got[0] != wireRate {
			t.Fatalf("frame errors = %v, want one rate", got)
		}
		if !tc.isOpen() || h.frameErrors("rate") != 7 {
			t.Fatalf("open %v rate %v", tc.isOpen(), h.frameErrors("rate"))
		}
		tc.close()
	})
}

func TestAFrameErrorOverAFullQueueIsDroppedAndCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.SendQueue = 2
		h := newHarness(t, opts)
		tc, c := h.dialConnection(false)
		for _, id := range []string{"a-", "b-", "c-"} {
			if !(roomSink{c: c}).TrySend(sizedFrame(t, id, 50)) {
				t.Fatalf("frame %s refused", id)
			}
			synctest.Wait()
		}
		_ = tc.conn.WriteMessage(gws.OpcodeText, []byte("hello"))
		synctest.Wait()
		if c.closing.Load() != 0 || h.sessionMetric("send_dropped_total", "kind", "control") != 1 {
			t.Fatalf("closing %d control drops %v", c.closing.Load(), h.sessionMetric("send_dropped_total", "kind", "control"))
		}
		tc.startReading()
		for range 3 {
			if f := tc.nextFrame(); f.GetEvent() == nil {
				t.Fatalf("frame = %v, want a queued event", f)
			}
		}
		synctest.Wait()
		if got := tc.drainFrames(); len(got) != 0 || !tc.isOpen() {
			t.Fatalf("frames after the queue %v, open %v", got, tc.isOpen())
		}
		tc.close()
	})
}

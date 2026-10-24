package session

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func budgetOptions() Options {
	opts := testOptions()
	opts.SendQueueBytes = 1024
	return opts
}

func sizedFrame(t *testing.T, id string, size int) []byte {
	t.Helper()
	for pad := size; pad >= 0; pad-- {
		event := &chatimv1.Event{Id: id + strings.Repeat("x", pad)}
		payload, err := proto.Marshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Event{Event: event}})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if len(payload) == size {
			return payload
		}
	}
	t.Fatalf("no frame of %d bytes", size)
	return nil
}

func (h *harness) dialConnection(reading bool) (*testClient, *connection) {
	h.t.Helper()
	known := h.liveConnections()
	tc := h.dial(reading)
	synctest.Wait()
	for c := range h.liveConnections() {
		if _, ok := known[c]; !ok {
			return tc, c
		}
	}
	h.t.Fatal("dialed connection is not live")
	return nil, nil
}

func (h *harness) liveConnections() map[*connection]struct{} {
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	out := make(map[*connection]struct{}, len(h.server.live))
	for c := range h.server.live {
		out[c] = struct{}{}
	}
	return out
}

func (h *harness) queuedBytes() float64 {
	for _, s := range h.server.Sources() {
		if s.Name == "send_queue_bytes" {
			return s.Read()
		}
	}
	h.t.Fatal("no send_queue_bytes metric")
	return 0
}

func (tc *testClient) startReading() { go tc.conn.ReadLoop() }

func paddedEvent(n int) (feed.Envelope, string) {
	id := fmt.Sprintf("e%d-%s", n, strings.Repeat("x", 300))
	return roomEvent("msg_created", "message", 7, id), id
}

func eventIDs(frames []*chatimv1.ServerFrame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		switch {
		case f.GetEvent() != nil:
			out = append(out, strings.SplitN(f.GetEvent().GetId(), "-", 2)[0])
		case f.GetResync() != nil:
			out = append(out, f.GetResync().GetReason().String())
		case f.GetReady() != nil:
			out = append(out, "ready")
		default:
			out = append(out, "other")
		}
	}
	return out
}

func TestASlowClientIsNeverClosedAndGetsOneResyncOnceItReadsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, budgetOptions())
		h.loader.set("t1", "slow", interest.Room{ID: 7, Ver: 1})
		h.loader.set("t1", "fast", interest.Room{ID: 7, Ver: 1})
		slow, slowConn := h.dialConnection(false)
		slow.auth(h.token("slow", time.Hour))
		synctest.Wait()
		fast := h.readyClient("fast")
		for n := 1; n <= 6; n++ {
			env, _ := paddedEvent(n)
			h.rooms.Route(env)
			synctest.Wait()
		}
		if got := slowConn.closing.Load(); got != 0 {
			t.Fatalf("slow connection closing with %d", got)
		}
		if got := eventIDs(fast.drainFrames()); !slices.Equal(got, []string{"e1", "e2", "e3", "e4", "e5", "e6"}) {
			t.Fatalf("fast frames = %v", got)
		}
		slow.startReading()
		synctest.Wait()
		got := eventIDs(slow.drainFrames())
		if len(got) < 2 || len(got) > 6 || got[0] != "ready" || !slices.Equal(got[1:], []string{"e1", "e2", "e3", "e4", "e5"}[:len(got)-1]) {
			t.Fatalf("slow frames before the resync = %v", got)
		}
		backlog := chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG.String()
		for n, want := range [][]string{{backlog, "e7"}, {"e8"}, {"e9"}} {
			env, _ := paddedEvent(n + 7)
			h.rooms.Route(env)
			synctest.Wait()
			if got := eventIDs(slow.drainFrames()); !slices.Equal(got, want) {
				t.Fatalf("slow frames for e%d = %v, want %v", n+7, got, want)
			}
		}
		if got := eventIDs(fast.drainFrames()); !slices.Equal(got, []string{"e7", "e8", "e9"}) {
			t.Fatalf("fast frames after = %v", got)
		}
		if !slow.isOpen() || !fast.isOpen() {
			t.Fatalf("slow open %v fast open %v", slow.isOpen(), fast.isOpen())
		}
		slow.close()
		fast.close()
		if got := h.queuedBytes(); got != 0 {
			t.Fatalf("queued bytes after close = %v, want 0", got)
		}
	})
}

func TestDrainedMeansBelowHalfOfTheFramesAndTheBytes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := budgetOptions()
		opts.SendQueue = 8
		h := newHarness(t, opts)
		tc, c := h.dialConnection(false)
		sink := roomSink{c: c}
		for i, size := range []int{20, 20, 20, 20, 20} {
			if !sink.TrySend(sizedFrame(t, fmt.Sprintf("f%d-", i), size)) {
				t.Fatalf("frame %d refused", i)
			}
			synctest.Wait()
			if want := i < 4; sink.Drained() != want {
				t.Fatalf("after %d frames (%d queued) drained = %v, want %v", i+1, len(c.out), sink.Drained(), want)
			}
		}
		tc.startReading()
		for range 5 {
			tc.nextFrame()
		}
		synctest.Wait()
		if !sink.Drained() {
			t.Fatal("an empty queue is not drained")
		}
		big, bigConn := h.dialConnection(false)
		bigSink := roomSink{c: bigConn}
		if !bigSink.TrySend(sizedFrame(t, "big-", 520)) || bigSink.Drained() {
			t.Fatalf("drained = %v with 520 of 1024 bytes queued", bigSink.Drained())
		}
		big.startReading()
		big.nextFrame()
		synctest.Wait()
		if !bigSink.Drained() {
			t.Fatal("not drained after the big frame was written")
		}
		tc.close()
		big.close()
	})
}

func TestTrySendRefusesAFrameOverTheBudgetWithoutClosing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, budgetOptions())
		tc, c := h.dialConnection(false)
		sink := roomSink{c: c}
		first, second, third := sizedFrame(t, "a-", 400), sizedFrame(t, "b-", 400), sizedFrame(t, "c-", 400)
		if !sink.TrySend(first) || !sink.TrySend(second) {
			t.Fatal("frames within the budget were refused")
		}
		if sink.TrySend(third) {
			t.Fatal("a frame over the budget was accepted")
		}
		if c.closing.Load() != 0 {
			t.Fatalf("TrySend closed the connection with %d", c.closing.Load())
		}
		if got := h.queuedBytes(); got != 800 {
			t.Fatalf("queued bytes = %v, want 800", got)
		}
		tc.startReading()
		tc.nextFrame()
		tc.nextFrame()
		synctest.Wait()
		if got := h.queuedBytes(); got != 0 {
			t.Fatalf("queued bytes after the writes = %v, want 0", got)
		}
		if !sink.TrySend(third) {
			t.Fatal("a frame was refused after the queue drained")
		}
		if got := tc.nextFrame().GetEvent().GetId(); !strings.HasPrefix(got, "c-") {
			t.Fatalf("frame = %q, want c-", got)
		}
		if !tc.isOpen() {
			t.Fatal("connection closed")
		}
		tc.close()
	})
}

func TestQueuedBytesAreReturnedWhenTheConnectionDies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, budgetOptions())
		tc, c := h.dialConnection(false)
		if !(roomSink{c: c}).TrySend(sizedFrame(t, "a-", 300)) || !(roomSink{c: c}).TrySend(sizedFrame(t, "b-", 300)) {
			t.Fatal("frames within the budget were refused")
		}
		if got := h.queuedBytes(); got != 600 {
			t.Fatalf("queued bytes = %v, want 600", got)
		}
		_ = tc.raw.Close()
		<-tc.serverDone
		if got := h.queuedBytes(); got != 0 {
			t.Fatalf("queued bytes after the connection died = %v, want 0", got)
		}
		if (roomSink{c: c}).TrySend(sizedFrame(t, "c-", 300)) || h.queuedBytes() != 0 {
			t.Fatal("a closed connection accepted a frame")
		}
	})
}

func TestAResponseOverTheByteBudgetIsDroppedAndTheConnectionStaysOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := budgetOptions()
		h := newHarness(t, opts)
		tc, c := h.dialConnection(false)
		tc.auth(h.token("u1", time.Hour))
		synctest.Wait()
		ready := int(h.queuedBytes())
		if !(roomSink{c: c}).TrySend(sizedFrame(t, "fill-", opts.SendQueueBytes-ready)) {
			t.Fatal("the fill frame was refused")
		}
		tc.history(1)
		synctest.Wait()
		if c.closing.Load() != 0 || h.sessionMetric("send_dropped_total", "kind", "response") != 1 {
			t.Fatalf("closing %d response drops %v", c.closing.Load(), h.sessionMetric("send_dropped_total", "kind", "response"))
		}
		tc.startReading()
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("first frame = %v, want ready", f)
		}
		if got := tc.nextFrame().GetEvent().GetId(); !strings.HasPrefix(got, "fill-") {
			t.Fatalf("second frame = %q, want fill-", got)
		}
		synctest.Wait()
		select {
		case resp := <-tc.responses:
			t.Fatalf("response %v delivered past the budget", resp)
		default:
		}
		tc.history(2)
		if resp := tc.next(); resp.GetId() != 2 || !tc.isOpen() {
			t.Fatalf("response %v open %v", resp, tc.isOpen())
		}
		tc.close()
		if got := h.queuedBytes(); got != 0 {
			t.Fatalf("queued bytes after close = %v, want 0", got)
		}
	})
}

func frameQueueOptions() Options {
	opts := testOptions()
	opts.SendQueue = 2
	return opts
}

func TestTrySendRefusesAFrameOverAFullFrameQueueWithoutClosing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, frameQueueOptions())
		tc, c := h.dialConnection(false)
		sink := roomSink{c: c}
		for _, id := range []string{"a-", "b-", "c-"} {
			if !sink.TrySend(sizedFrame(t, id, 50)) {
				t.Fatalf("frame %s refused before the queue was full", id)
			}
			synctest.Wait()
		}
		if sink.TrySend(sizedFrame(t, "d-", 50)) {
			t.Fatal("a frame over the full queue was accepted")
		}
		if c.closing.Load() != 0 {
			t.Fatalf("TrySend closed the connection with %d", c.closing.Load())
		}
		tc.startReading()
		for range 3 {
			tc.nextFrame()
		}
		if !tc.isOpen() {
			t.Fatal("connection closed")
		}
		tc.close()
	})
}

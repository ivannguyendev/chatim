package session

import (
	"bytes"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

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

func TestEventsOverTheByteBudgetCloseOnlyThatConnectionWith4008(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, budgetOptions())
		slow, slowConn := h.dialConnection(false)
		fast, fastConn := h.dialConnection(true)
		shared := [][]byte{sizedFrame(t, "e1-", 400), sizedFrame(t, "e2-", 400), sizedFrame(t, "e3-", 400)}
		copies := make([][]byte, len(shared))
		for i, frame := range shared {
			copies[i] = bytes.Clone(frame)
		}
		start := time.Now()
		for _, frame := range shared {
			roomSink{c: slowConn}.Send(frame)
			roomSink{c: fastConn}.Send(frame)
			synctest.Wait()
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("senders blocked for %v by a slow client", elapsed)
		}
		if got := slowConn.closing.Load(); got != uint32(CodeSlowClient) {
			t.Fatalf("slow connection closing = %d, want %d", got, CodeSlowClient)
		}
		for i := range shared {
			if got := fast.nextFrame().GetEvent().GetId(); !strings.HasPrefix(got, "e"+string(rune('1'+i))+"-") {
				t.Fatalf("fast frame %d = %q", i, got)
			}
		}
		slow.startReading()
		if got := slow.waitClosed(); got != CodeSlowClient {
			t.Fatalf("slow close code = %d, want %d", got, CodeSlowClient)
		}
		for i := range 2 {
			if got := (<-slow.frames).GetEvent().GetId(); !strings.HasPrefix(got, "e"+string(rune('1'+i))+"-") {
				t.Fatalf("slow frame %d = %q", i, got)
			}
		}
		if h.closes(CodeSlowClient) != 1 || !fast.isOpen() {
			t.Fatalf("closes 4008 = %d, fast open = %v", h.closes(CodeSlowClient), fast.isOpen())
		}
		for i := range shared {
			if !bytes.Equal(shared[i], copies[i]) {
				t.Fatalf("shared frame %d was modified", i)
			}
		}
		fast.close()
		if got := h.queuedBytes(); got != 0 {
			t.Fatalf("queued bytes after close = %v, want 0", got)
		}
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
		roomSink{c: c}.Send(sizedFrame(t, "a-", 300))
		roomSink{c: c}.Send(sizedFrame(t, "b-", 300))
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

func TestAResponseOverTheByteBudgetClosesWith4008(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := budgetOptions()
		h := newHarness(t, opts)
		tc, c := h.dialConnection(false)
		tc.auth(h.token("u1", time.Hour))
		synctest.Wait()
		ready := int(h.queuedBytes())
		roomSink{c: c}.Send(sizedFrame(t, "fill-", opts.SendQueueBytes-ready))
		tc.history(1)
		synctest.Wait()
		if got := c.closing.Load(); got != uint32(CodeSlowClient) {
			t.Fatalf("closing = %d, want %d", got, CodeSlowClient)
		}
		tc.startReading()
		if got := tc.waitClosed(); got != CodeSlowClient {
			t.Fatalf("close code = %d, want %d", got, CodeSlowClient)
		}
		if f := <-tc.frames; f.GetReady() == nil {
			t.Fatalf("first frame = %v, want ready", f)
		}
		if got := (<-tc.frames).GetEvent().GetId(); !strings.HasPrefix(got, "fill-") {
			t.Fatalf("second frame = %q, want fill-", got)
		}
		select {
		case resp := <-tc.responses:
			t.Fatalf("response %v delivered past the budget", resp)
		default:
		}
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

func TestAFullFrameQueueClosesWith4008AndDropsLaterFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, frameQueueOptions())
		tc, c := h.dialConnection(false)
		sink := roomSink{c: c}
		for _, id := range []string{"a-", "b-", "c-"} {
			sink.Send(sizedFrame(t, id, 50))
			synctest.Wait()
		}
		if c.closing.Load() != 0 {
			t.Fatalf("closed with %d before the queue was full", c.closing.Load())
		}
		start := time.Now()
		sink.Send(sizedFrame(t, "d-", 50))
		if got := c.closing.Load(); got != uint32(CodeSlowClient) || time.Since(start) != 0 {
			t.Fatalf("closing = %d after %v, want %d at once", got, time.Since(start), CodeSlowClient)
		}
		before := h.queuedBytes()
		sink.Send(sizedFrame(t, "late-", 50))
		if got := h.queuedBytes(); got != before {
			t.Fatalf("queued bytes = %v after closing, want %v", got, before)
		}
		tc.startReading()
		if got := tc.waitClosed(); got != CodeSlowClient {
			t.Fatalf("close code = %d, want %d", got, CodeSlowClient)
		}
		for _, want := range []string{"a-", "b-", "c-"} {
			if got := (<-tc.frames).GetEvent().GetId(); !strings.HasPrefix(got, want) {
				t.Fatalf("frame = %q, want %s", got, want)
			}
		}
		select {
		case f := <-tc.frames:
			t.Fatalf("frame %v delivered after the 4008 decision", f)
		default:
		}
		if h.closes(CodeSlowClient) != 1 || h.closes(CodeAbnormal) != 0 || h.queuedBytes() != 0 {
			t.Fatalf("closes 4008 = %d, aborts = %d, queued = %v", h.closes(CodeSlowClient), h.closes(CodeAbnormal), h.queuedBytes())
		}
	})
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

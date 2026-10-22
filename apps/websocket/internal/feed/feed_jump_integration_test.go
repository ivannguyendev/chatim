package feed

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

const (
	itStalled    = 40
	itAfterJump  = 20
	itStallFor   = 3 * time.Second
	itReplayFast = 2 * time.Second
)

type jumpHandler struct {
	gate   chan struct{}
	first  chan struct{}
	jumped chan Jump
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	seqs   []uint64
	after  int
	jump   uint64
}

func (h *jumpHandler) Event(e Envelope) {
	h.once.Do(func() { close(h.first) })
	<-h.gate
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seqs = append(h.seqs, e.Seq)
	if h.jump > 0 && e.Seq > h.jump {
		if h.after++; h.after == itAfterJump {
			close(h.done)
		}
	}
}

func (h *jumpHandler) Publish(Envelope) {}

func (h *jumpHandler) Jumped(j Jump) {
	h.mu.Lock()
	h.jump = j.Seq
	h.mu.Unlock()
	h.jumped <- j
}

func TestFeedSkipsToTheStreamEndAfterAStallLongerThanTheMaxAge(t *testing.T) {
	url := os.Getenv("CHATIM_IT_NATS_URL")
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	js, cfg := itStreams(t, url)
	cfg.ReplayMaxAge = itReplayFast
	h := &jumpHandler{gate: make(chan struct{}), first: make(chan struct{}), jumped: make(chan Jump, 4), done: make(chan struct{})}
	f, err := New(cfg, h, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()
	b, err := Dial(ctx, NATSConfig{URL: url, Name: "feed-jump-it"}, quietLog())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := f.Start(ctx, b); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = f.Stop(context.Background()) }()

	publishEvents(t, js, cfg.EventRoot, 1, itStalled)
	waitFor(t, h.first, "first event")
	time.Sleep(itStallFor)
	close(h.gate)
	var j Jump
	select {
	case j = <-h.jumped:
	case <-time.After(itTimeout):
		t.Fatal("the feed did not skip after the stall")
	}
	if j.Reason != JumpLagTooOld || j.Seq != itStalled {
		t.Fatalf("jump = %+v, want lag_too_old at seq %d", j, itStalled)
	}
	publishEvents(t, js, cfg.EventRoot, itStalled+1, itStalled+itAfterJump)
	waitFor(t, h.done, "events published after the skip")

	h.mu.Lock()
	skipped := 0
	for _, seq := range h.seqs {
		if seq <= j.Seq {
			skipped++
		}
	}
	got := len(h.seqs)
	h.mu.Unlock()
	if skipped >= itStalled || got != skipped+itAfterJump {
		t.Fatalf("handled %d stalled and %d in total, want part of the %d stalled skipped and all %d new", skipped, got, itStalled, itAfterJump)
	}
	if f.streams[0].jumps.Load() != 1 || f.streams[0].jumpFailures.Load() != 0 || f.streams[1].jumps.Load() != 0 {
		t.Fatalf("jumps evt %d failures %d ws %d", f.streams[0].jumps.Load(), f.streams[0].jumpFailures.Load(), f.streams[1].jumps.Load())
	}
	select {
	case extra := <-h.jumped:
		t.Fatalf("a second skip %+v after the feed caught up", extra)
	default:
	}
}

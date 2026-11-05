package slotmap

import (
	"slices"
	"sync"
	"testing"
)

type loadRecorder struct {
	mu    sync.Mutex
	loads [][]string
}

func (l *loadRecorder) record(addrs []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loads = append(l.loads, slices.Sorted(slices.Values(addrs)))
}

func (l *loadRecorder) seen() [][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.loads)
}

func TestResolverReportsTheLiveAddressesAfterEverySuccessfulLoad(t *testing.T) {
	rec := &loadRecorder{}
	g := newResolverRig(t, ResolverConfig{OnLoad: rec.record})
	g.heartbeat("chat-a", nodeAddr("chat-a"), 0)
	g.heartbeat("chat-b", nodeAddr("chat-b"), 0)
	g.refresh(t)
	_ = g.mr.Set(NodeKey("chat-b"), "chat-b:9100")
	g.refresh(t)
	g.mr.SetError("LOADING redis is loading")
	if err := g.r.Refresh(t.Context()); err == nil {
		t.Fatal("Refresh with failing redis = nil, want an error")
	}
	g.mr.SetError("")
	g.mr.Del(NodeKey("chat-a"))
	g.mr.Del(NodeKey("chat-b"))
	g.refresh(t)
	want := [][]string{{"chat-a:9000", "chat-b:9000"}, {"chat-a:9000", "chat-b:9100"}, {}}
	got := rec.seen()
	if len(got) != len(want) {
		t.Fatalf("OnLoad calls = %v, want %v", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("OnLoad call %d = %v, want %v", i, got[i], want[i])
		}
	}
}

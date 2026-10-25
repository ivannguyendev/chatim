package session

import (
	"context"
	"slices"
	"sync"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type presenceTouch struct {
	tenant, user string
	at           time.Time
}

type presenceAsk struct {
	tenant string
	users  []string
}

type fakePresence struct {
	mu      sync.Mutex
	touches []presenceTouch
	asks    []presenceAsk
	online  map[string]bool
	err     error
	hold    chan struct{}
}

func (p *fakePresence) Touch(tenant, user string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.touches = append(p.touches, presenceTouch{tenant: tenant, user: user, at: now})
}

func (p *fakePresence) Answer(ctx context.Context, tenant string, users []string) ([]*chatimv1.PresenceEntry, error) {
	p.mu.Lock()
	p.asks = append(p.asks, presenceAsk{tenant: tenant, users: slices.Clone(users)})
	hold, err := p.hold, p.err
	entries := make([]*chatimv1.PresenceEntry, len(users))
	for i, u := range users {
		entries[i] = &chatimv1.PresenceEntry{UserId: u, Online: p.online[tenant+"/"+u]}
	}
	p.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (p *fakePresence) setOnline(tenant, user string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.online == nil {
		p.online = map[string]bool{}
	}
	p.online[tenant+"/"+user] = true
}

func (p *fakePresence) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *fakePresence) holdAnswers() chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hold = make(chan struct{})
	return p.hold
}

func (p *fakePresence) release(hold chan struct{}) {
	p.mu.Lock()
	p.hold = nil
	p.mu.Unlock()
	close(hold)
}

func (p *fakePresence) askCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.asks)
}

func (p *fakePresence) lastAsk() presenceAsk {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.asks) == 0 {
		return presenceAsk{}
	}
	return p.asks[len(p.asks)-1]
}

func (p *fakePresence) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.touches)
}

func (p *fakePresence) last() presenceTouch {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.touches) == 0 {
		return presenceTouch{}
	}
	return p.touches[len(p.touches)-1]
}

func (tc *testClient) watch(users ...string) {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_WatchPresence{WatchPresence: &chatimv1.WatchPresenceFrame{UserIds: users}}})
}

func (tc *testClient) nextPresence() []*chatimv1.PresenceEntry {
	tc.t.Helper()
	f := tc.nextFrame()
	if f.GetPresence() == nil {
		tc.t.Fatalf("frame = %v, want presence", f)
	}
	return f.GetPresence().GetEntries()
}

func (tc *testClient) expectPresence(want ...string) {
	tc.t.Helper()
	entries := tc.nextPresence()
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.GetUserId()
		if e.GetOnline() {
			got[i] += "+"
		}
	}
	if !slices.Equal(got, want) {
		tc.t.Fatalf("presence = %v, want %v (+ = online)", got, want)
	}
}

func (h *harness) presenceMetric(name, reason string) float64 {
	h.t.Helper()
	for _, s := range h.server.Sources() {
		if s.Name == name && (reason == "" || s.Labels["reason"] == reason) {
			return s.Read()
		}
	}
	h.t.Fatalf("no metric %s %s", name, reason)
	return 0
}

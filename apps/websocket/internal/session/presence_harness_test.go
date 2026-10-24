package session

import (
	"sync"
	"time"
)

type presenceTouch struct {
	tenant, user string
	at           time.Time
}

type fakePresence struct {
	mu      sync.Mutex
	touches []presenceTouch
}

func (p *fakePresence) Touch(tenant, user string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.touches = append(p.touches, presenceTouch{tenant: tenant, user: user, at: now})
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

package memberwatch

import (
	"errors"
	"slices"
	"strings"
	"sync"
)

type fakeBus struct {
	mu       sync.Mutex
	subs     []*fakeSub
	patterns []string
	failOn   string
}

type fakeSub struct {
	bus     *fakeBus
	pattern string
	inbox   chan string
}

func (b *fakeBus) Subscribe(subject string, handle func(string)) (subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subject == b.failOn {
		return nil, errors.New("subscribe refused")
	}
	s := &fakeSub{bus: b, pattern: subject, inbox: make(chan string, 16)}
	b.subs = append(b.subs, s)
	b.patterns = append(b.patterns, subject)
	go func() {
		for subj := range s.inbox {
			handle(subj)
		}
	}()
	return s, nil
}

func (s *fakeSub) Unsubscribe() error {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.bus.subs = slices.DeleteFunc(s.bus.subs, func(o *fakeSub) bool { return o == s })
	close(s.inbox)
	return nil
}

func (b *fakeBus) publish(subject string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if matches(s.pattern, subject) {
			s.inbox <- subject
		}
	}
}

func (b *fakeBus) live() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

func matches(pattern, subject string) bool {
	p, s := strings.Split(pattern, "."), strings.Split(subject, ".")
	if len(p) != len(s) {
		return false
	}
	for i := range p {
		if p[i] != "*" && p[i] != s[i] {
			return false
		}
	}
	return true
}

type forgotten struct {
	mu    sync.Mutex
	rooms []uint64
}

func (f *forgotten) forget(room uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rooms = append(f.rooms, room)
}

func (f *forgotten) got() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rooms)
}

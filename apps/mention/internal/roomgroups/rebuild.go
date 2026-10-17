package roomgroups

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type background struct {
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  map[uint64]struct{}
	closed   bool
	failures atomic.Uint64
}

func newBackground() background {
	ctx, cancel := context.WithCancel(context.Background())
	return background{ctx: ctx, cancel: cancel, running: make(map[uint64]struct{})}
}

func (s *Sets) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func (s *Sets) Failures() uint64 { return s.failures.Load() }

func (s *Sets) large(tenant string, room uint64) Set {
	e, ok := s.cache.Get(room)
	s.count(ok)
	if !ok || time.Since(e.BuiltAt) >= s.cfg.Rebuild {
		s.kick(tenant, room)
	}
	if !ok {
		return Set{}
	}
	return Set{Groups: e.Groups, Filtered: true}
}

func (s *Sets) kick(tenant string, room uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.running[room]; busy || s.closed || len(s.running) >= MaxRebuilds {
		return
	}
	s.running[room] = struct{}{}
	s.wg.Add(1)
	go s.rebuild(tenant, room)
}

func (s *Sets) rebuild(tenant string, room uint64) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.running, room)
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(s.ctx, RebuildTimeout)
	defer cancel()
	mark := s.cache.Mark()
	groups, err := s.build(ctx, tenant, room)
	if err != nil {
		s.failures.Add(1)
		s.log.WarnContext(ctx, "room groups rebuild failed", "room", room, "err", err)
		return
	}
	s.cache.Put(mark, room, Entry{Groups: groups, BuiltAt: time.Now()})
}

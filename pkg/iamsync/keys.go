package iamsync

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func (s *Syncer) syncKeys(ctx context.Context) {
	started := time.Now()
	status, _, body, err := s.fetch(ctx, s.cfg.JWKSURL, http.Header{})
	if err == nil {
		s.mu.Lock()
		s.keysRefused = refusal(status)
		s.mu.Unlock()
		if status != http.StatusOK {
			err = fmt.Errorf("iamsync: jwks answered %d", status)
		}
	}
	if err == nil {
		var conflicts int
		conflicts, err = s.keys.SetDynamic(body)
		s.stats.keyConflicts.Add(uint64(max(conflicts, 0)))
	}
	if err != nil {
		s.stats.keyErrors.Add(1)
		s.log.DebugContext(ctx, "iam jwks fetch failed", "error", err)
		return
	}
	s.mu.Lock()
	s.lastKeys, s.keysStartedOK, s.keysReady = time.Now(), started, true
	s.mu.Unlock()
}

func (s *Syncer) UnknownKey(kid string) bool {
	now := time.Now()
	s.mu.Lock()
	first, ok := s.seen[kid]
	if !ok {
		if len(s.seen) >= maxSeenKeys {
			clear(s.seen)
		}
		s.seen[kid], first = now, now
	}
	retry := s.keysStartedOK.Before(first)
	ask := retry && now.Sub(s.lastKeyAsk) >= s.cfg.KeysMinGap
	if ask {
		s.lastKeyAsk = now
	}
	s.mu.Unlock()
	if ask {
		select {
		case s.keysWake <- struct{}{}:
		default:
		}
	}
	return retry
}

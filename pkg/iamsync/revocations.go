package iamsync

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ivannguyendev/chatim/pkg/revocation"
)

type sharedValue struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

func (s *Syncer) syncList(ctx context.Context) {
	if err := s.fetchList(ctx); err != nil {
		s.stats.listErrors.Add(1)
		s.log.DebugContext(ctx, "iam revocation list fetch failed", "error", err)
		s.readShared(ctx)
	}
	s.list.Prune(time.Now())
}

func (s *Syncer) fetchList(ctx context.Context) error {
	header := http.Header{"Authorization": {"Bearer " + s.cfg.Secret}}
	s.mu.Lock()
	if s.etag != "" {
		header.Set("If-None-Match", s.etag)
	}
	s.mu.Unlock()
	status, h, body, err := s.fetch(ctx, s.cfg.InternalURL+revocationsPath, header)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.listRefused = refusal(status)
	s.mu.Unlock()
	now := time.Now()
	switch status {
	case http.StatusNotModified:
	case http.StatusOK:
		var snap revocation.Snapshot
		if err := json.Unmarshal(body, &snap); err != nil {
			return fmt.Errorf("iamsync: decode revocation list: %w", err)
		}
		s.apply(snap, now)
		s.mu.Lock()
		s.etag, s.body = h.Get("ETag"), body
		s.mu.Unlock()
	default:
		return fmt.Errorf("iamsync: revocation list answered %d", status)
	}
	s.mu.Lock()
	s.lastList, s.listReady = now, true
	kept := s.body
	s.mu.Unlock()
	s.writeShared(ctx, now, kept)
	return nil
}

func (s *Syncer) apply(snap revocation.Snapshot, now time.Time) {
	added := s.list.Merge(snap, now)
	if added.Empty() {
		return
	}
	s.mu.Lock()
	notify := s.onAdded
	s.mu.Unlock()
	if notify != nil {
		notify(added)
	}
}

func (s *Syncer) writeShared(ctx context.Context, now time.Time, body []byte) {
	if s.shared == nil || body == nil {
		return
	}
	value, err := json.Marshal(sharedValue{FetchedAt: now, Snapshot: body})
	if err == nil {
		putCtx, cancel := context.WithTimeout(ctx, s.cfg.HTTPTimeout)
		err = s.shared.Put(putCtx, value, s.cfg.SharedTTL)
		cancel()
	}
	if err != nil {
		s.stats.sharedWriteErrors.Add(1)
	}
}

func (s *Syncer) readShared(ctx context.Context) {
	if s.shared == nil {
		return
	}
	getCtx, cancel := context.WithTimeout(ctx, s.cfg.HTTPTimeout)
	raw, err := s.shared.Get(getCtx)
	cancel()
	if err != nil {
		s.stats.sharedErrors.Add(1)
		return
	}
	if raw == nil {
		return
	}
	hash := sha256.Sum256(raw)
	s.mu.Lock()
	unchanged := hash == s.sharedHash
	s.mu.Unlock()
	if unchanged {
		s.stats.sharedUnchanged.Add(1)
		return
	}
	var value sharedValue
	var snap revocation.Snapshot
	if json.Unmarshal(raw, &value) != nil || json.Unmarshal(value.Snapshot, &snap) != nil {
		s.stats.sharedErrors.Add(1)
		return
	}
	now := time.Now()
	s.apply(snap, now)
	fetched := value.FetchedAt
	if fetched.After(now) {
		fetched = now
	}
	s.mu.Lock()
	s.sharedHash, s.listReady = hash, true
	if fetched.After(s.lastList) {
		s.lastList = fetched
	}
	s.mu.Unlock()
	s.stats.sharedMerged.Add(1)
}

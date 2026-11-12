package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type MessageFiles struct {
	mu    sync.Mutex
	pairs map[string]domain.MessageFile
}

func NewMessageFiles() *MessageFiles {
	return &MessageFiles{pairs: map[string]domain.MessageFile{}}
}

func (s *MessageFiles) Attach(_ context.Context, pairs []domain.MessageFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range pairs {
		id := p.MessageKey + "|" + p.FileKey
		sent := p.SentAt.Truncate(time.Millisecond).UTC()
		if had, taken := s.pairs[id]; taken {
			had.Room, had.Kind, had.SentAt = p.Room, p.Kind, sent
			s.pairs[id] = had
			continue
		}
		p.State, p.CreatedAt, p.DeletedAt, p.SentAt = domain.PairLive, p.CreatedAt.UTC(), time.Time{}, sent
		s.pairs[id] = p
	}
	return nil
}

func (s *MessageFiles) Active(_ context.Context, tenant, messageKey string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for _, p := range s.pairs {
		if p.Tenant == tenant && p.MessageKey == messageKey && p.State == domain.PairLive {
			keys = append(keys, p.FileKey)
		}
	}
	return keys, nil
}

func (s *MessageFiles) Release(_ context.Context, tenant, messageKey string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, p := range s.pairs {
		if p.Tenant == tenant && p.MessageKey == messageKey && p.State == domain.PairLive {
			p.State, p.DeletedAt = domain.PairDeleted, at.UTC()
			s.pairs[id] = p
		}
	}
	return nil
}

func (s *MessageFiles) StillUsed(_ context.Context, fileKeys []string, exceptMessage string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	used := make(map[string]bool, len(fileKeys))
	for _, k := range fileKeys {
		used[k] = false
	}
	for _, p := range s.pairs {
		if _, asked := used[p.FileKey]; asked && p.State == domain.PairLive && p.MessageKey != exceptMessage {
			used[p.FileKey] = true
		}
	}
	return used, nil
}

package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type Uploads struct {
	mu   sync.Mutex
	docs map[string]domain.Upload
	now  func() time.Time
}

func NewUploads() *Uploads {
	return &Uploads{docs: map[string]domain.Upload{}, now: time.Now}
}

func (s *Uploads) Insert(_ context.Context, u domain.Upload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.docs[u.Key]; taken {
		return domain.ErrRetryLater
	}
	s.docs[u.Key] = u
	return nil
}

func (s *Uploads) Get(_ context.Context, key string) (domain.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.docs[key]
	if !ok {
		return domain.Upload{}, domain.ErrUploadNotFound
	}
	return u, nil
}

func (s *Uploads) Transition(_ context.Context, key string, from domain.State, fromVer int64, to domain.State, hash string) (domain.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.docs[key]
	if !ok || u.State != from || u.Ver != fromVer {
		return domain.Upload{}, domain.ErrRetryLater
	}
	now := s.now().UTC()
	u.State, u.Ver, u.UpdatedAt = to, u.Ver+1, now
	if to == domain.StateUploaded {
		u.CompletedAt = now
		if hash != "" {
			u.Hash = hash
		}
	}
	s.docs[key] = u
	return u, nil
}

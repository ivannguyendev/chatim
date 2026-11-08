package memstore

import (
	"context"
	"slices"
	"strings"
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

func (s *Uploads) ListOwned(_ context.Context, tenant, user, before string, kinds domain.Kinds, limit int) ([]domain.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Upload
	for _, u := range s.docs {
		if u.Tenant == tenant && u.User == user && u.State == domain.StateUploaded && (before == "" || u.Key < before) && kinds.Match(u.ContentType) {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b domain.Upload) int { return strings.Compare(b.Key, a.Key) })
	return out[:min(len(out), limit)], nil
}

func (s *Uploads) GetMany(_ context.Context, tenant string, keys []string) (map[string]domain.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]domain.Upload, len(keys))
	for _, k := range keys {
		if u, ok := s.docs[k]; ok && u.Tenant == tenant {
			out[k] = locationOf(u)
		}
	}
	return out, nil
}

func locationOf(u domain.Upload) domain.Upload {
	return domain.Upload{
		Key: u.Key, Tenant: u.Tenant, User: u.User, ContentType: u.ContentType, Size: u.Size, State: u.State, Ver: u.Ver,
		CreatedAt: u.CreatedAt, Bucket: u.Bucket, AttachedAt: u.AttachedAt, RetiredAt: u.RetiredAt,
	}
}

func (s *Uploads) MarkMoved(_ context.Context, key string, ver int64, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.docs[key]
	if !ok || u.Ver != ver {
		return domain.ErrRetryLater
	}
	if u.AttachedAt.IsZero() {
		u.AttachedAt = at.UTC()
	}
	u.Bucket, u.Ver, u.UpdatedAt = domain.BucketCompleted, u.Ver+1, at.UTC()
	s.docs[key] = u
	return nil
}

func (s *Uploads) Retire(_ context.Context, keys []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if u, ok := s.docs[k]; ok && u.RetiredAt.IsZero() {
			u.RetiredAt = at.UTC()
			s.docs[k] = u
		}
	}
	return nil
}

func (s *Uploads) Unretire(_ context.Context, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if u, ok := s.docs[k]; ok {
			u.RetiredAt = time.Time{}
			s.docs[k] = u
		}
	}
	return nil
}

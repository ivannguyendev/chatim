package upload

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/objectstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

type Objects interface {
	StartMultipart(ctx context.Context, key, contentType string) (string, error)
	SignPart(ctx context.Context, key, uploadID string, number int32, size int64, ttl time.Duration) (string, error)
	Complete(ctx context.Context, key, uploadID string, parts []domain.UploadedPart) error
	Abort(ctx context.Context, key, uploadID string) error
	Head(ctx context.Context, key string) (int64, error)
	Delete(ctx context.Context, key string) error
}

type Config struct {
	Limits     domain.Limits
	PartURLTTL time.Duration
}

type Deps struct {
	Uploads store.Uploads
	Objects Objects
	Ready   func() bool
	Now     func() time.Time
	Random  func() [10]byte
}

type Caller struct{ Tenant, User string }

type StartInput struct {
	FileName       string
	ContentType    string
	Size           int64
	Dim            []int32
	Dur            int64
	BytesPerSecond int64
}

type PartURL struct {
	Number int32
	URL    string
}

type Started struct {
	Upload    domain.Upload
	Parts     []domain.Part
	URLs      []PartURL
	ExpiresAt time.Time
}

type Service struct {
	cfg       Config
	deps      Deps
	started   atomic.Uint64
	completed atomic.Uint64
}

func (s *Service) Started() uint64 { return s.started.Load() }

func (s *Service) Completed() uint64 { return s.completed.Load() }

func New(cfg Config, deps Deps) (*Service, error) {
	if err := cfg.Limits.Validate(); err != nil {
		return nil, err
	}
	if deps.Uploads == nil || deps.Objects == nil || cfg.PartURLTTL <= 0 {
		return nil, errors.New("upload: needs uploads, objects and a positive part URL lifetime")
	}
	if deps.Ready == nil {
		deps.Ready = func() bool { return true }
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Random == nil {
		deps.Random = randomKeyPart
	}
	return &Service{cfg: cfg, deps: deps}, nil
}

func randomKeyPart() [10]byte {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return b
}

func (s *Service) Start(ctx context.Context, who Caller, in StartInput) (Started, error) {
	if !s.deps.Ready() {
		return Started{}, domain.ErrNotReady
	}
	contentType, err := domain.ValidateStart(in.FileName, in.ContentType, in.Size, s.cfg.Limits)
	if err != nil {
		return Started{}, err
	}
	if err := domain.ValidateFileFacts(in.Dim, in.Dur); err != nil {
		return Started{}, err
	}
	partSize, parts, err := domain.PlanParts(in.Size, in.BytesPerSecond, s.cfg.Limits)
	if err != nil {
		return Started{}, err
	}
	now := s.deps.Now().UTC()
	key := domain.NewObjectKey(who.Tenant, who.User, now, s.deps.Random())
	uploadID, err := s.deps.Objects.StartMultipart(ctx, key, contentType)
	if err != nil {
		return Started{}, storageError(err)
	}
	u := domain.Upload{
		Key: key, Tenant: who.Tenant, User: who.User, FileName: in.FileName, ContentType: contentType,
		Size: in.Size, Dim: in.Dim, Dur: in.Dur, PartSize: partSize, PartCount: len(parts), UploadID: uploadID,
		State: domain.StateUploading, Ver: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.deps.Uploads.Insert(ctx, u); err != nil {
		_ = s.deps.Objects.Abort(context.WithoutCancel(ctx), key, uploadID)
		return Started{}, err
	}
	first := make([]int32, 0, s.cfg.Limits.MaxParallel)
	for _, p := range parts[:min(len(parts), s.cfg.Limits.MaxParallel)] {
		first = append(first, p.Number)
	}
	urls, expires, err := s.sign(ctx, u, first)
	if err != nil {
		return Started{}, err
	}
	s.started.Add(1)
	return Started{Upload: u, Parts: parts, URLs: urls, ExpiresAt: expires}, nil
}

func (s *Service) PartURLs(ctx context.Context, who Caller, key string, numbers []int32) ([]PartURL, time.Time, error) {
	u, err := s.owned(ctx, who, key)
	if err != nil {
		return nil, time.Time{}, err
	}
	if u.State != domain.StateUploading {
		return nil, time.Time{}, domain.ErrNotUploading
	}
	if err := domain.CheckPartNumbers(numbers, u.PartCount, s.cfg.Limits.MaxParallel); err != nil {
		return nil, time.Time{}, err
	}
	return s.sign(ctx, u, numbers)
}

func (s *Service) owned(ctx context.Context, who Caller, key string) (domain.Upload, error) {
	if !s.deps.Ready() {
		return domain.Upload{}, domain.ErrNotReady
	}
	if !domain.OwnsKey(key, who.Tenant, who.User) {
		return domain.Upload{}, domain.ErrUploadNotFound
	}
	u, err := s.deps.Uploads.Get(ctx, key)
	if err != nil {
		return domain.Upload{}, err
	}
	if u.Tenant != who.Tenant || u.User != who.User {
		return domain.Upload{}, domain.ErrUploadNotFound
	}
	return u, nil
}

func (s *Service) sign(ctx context.Context, u domain.Upload, numbers []int32) ([]PartURL, time.Time, error) {
	expires := s.deps.Now().UTC().Add(s.cfg.PartURLTTL)
	urls := make([]PartURL, 0, len(numbers))
	for _, n := range numbers {
		size := min(u.PartSize, u.Size-int64(n-1)*u.PartSize)
		url, err := s.deps.Objects.SignPart(ctx, u.Key, u.UploadID, n, size, s.cfg.PartURLTTL)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("%w: %w", domain.ErrRetryLater, err)
		}
		urls = append(urls, PartURL{Number: n, URL: url})
	}
	return urls, expires, nil
}

func storageError(err error) error {
	if errors.Is(err, objectstore.ErrInvalidPart) {
		return fmt.Errorf("%w: %w", domain.ErrPartRejected, err)
	}
	return fmt.Errorf("%w: %w", domain.ErrRetryLater, err)
}

package gallery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
)

const MaxPageSize = 100

type Uploads interface {
	Get(ctx context.Context, key string) (domain.Upload, error)
	ListOwned(ctx context.Context, tenant, user, before string, kinds domain.Kinds, limit int) ([]domain.Upload, error)
}

type Config struct {
	SignatureTTL time.Duration
	Retention    time.Duration
	Thumbnails   domain.Thumbnails
}

type Deps struct {
	Uploads Uploads
	Objects download.Objects
	Ready   func() bool
	Now     func() time.Time
}

type Input struct {
	Kinds    domain.Kinds
	Before   string
	PageSize int
}

type File struct {
	Upload       domain.Upload
	Expired      bool
	URL          string
	ThumbnailURL string
}

type Page struct {
	Files      []File
	NextBefore string
	ExpiresAt  time.Time
}

type Service struct {
	cfg    Config
	deps   Deps
	signer download.Signer
}

func New(cfg Config, deps Deps) (*Service, error) {
	if deps.Uploads == nil || deps.Objects == nil || cfg.SignatureTTL <= 0 || cfg.Retention <= 0 {
		return nil, errors.New("gallery: needs uploads, objects, a positive signature lifetime and retention")
	}
	if deps.Ready == nil {
		deps.Ready = func() bool { return true }
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	signer := download.Signer{Objects: deps.Objects, TTL: cfg.SignatureTTL, Thumbnails: cfg.Thumbnails}
	return &Service{cfg: cfg, deps: deps, signer: signer}, nil
}

func (s *Service) List(ctx context.Context, who domain.Caller, in Input) (Page, error) {
	if !s.deps.Ready() {
		return Page{}, domain.ErrNotReady
	}
	if in.PageSize < 1 || in.PageSize > MaxPageSize {
		return Page{}, fmt.Errorf("%w: page size must be 1..%d", domain.ErrInvalid, MaxPageSize)
	}
	if err := s.checkBefore(ctx, who, in.Before); err != nil {
		return Page{}, err
	}
	bound := in.Before
	if bound == "" {
		bound = domain.PublicKeyPrefix(who.Tenant, who.User)
	}
	found, err := s.deps.Uploads.ListOwned(ctx, who.Tenant, who.User, bound, in.Kinds, in.PageSize)
	if err != nil {
		return Page{}, err
	}
	now := s.deps.Now().UTC()
	page := Page{Files: make([]File, 0, len(found)), ExpiresAt: now.Add(s.cfg.SignatureTTL)}
	for _, u := range found {
		f, err := s.file(ctx, u, now)
		if err != nil {
			return Page{}, err
		}
		page.Files = append(page.Files, f)
	}
	if len(found) == in.PageSize {
		page.NextBefore = found[len(found)-1].Key
	}
	return page, nil
}

func (s *Service) checkBefore(ctx context.Context, who domain.Caller, before string) error {
	if before == "" {
		return nil
	}
	if strings.HasPrefix(before, domain.PublicKeyPrefix(who.Tenant, who.User)) {
		return fmt.Errorf("%w: before is not one of your uploads", domain.ErrInvalid)
	}
	u, err := s.deps.Uploads.Get(ctx, before)
	switch {
	case errors.Is(err, domain.ErrUploadNotFound):
		return fmt.Errorf("%w: before is not one of your uploads", domain.ErrInvalid)
	case err != nil:
		return err
	case u.Tenant != who.Tenant || u.User != who.User:
		return fmt.Errorf("%w: before is not one of your uploads", domain.ErrInvalid)
	}
	return nil
}

func (s *Service) file(ctx context.Context, u domain.Upload, now time.Time) (File, error) {
	bucket := u.Bucket.OrProcessing()
	if bucket == domain.BucketProcessing && !u.CreatedAt.Add(s.cfg.Retention).After(now) {
		return File{Upload: u, Expired: true}, nil
	}
	signed, err := s.signer.Sign(ctx, bucket, domain.File{Key: u.Key, Name: u.FileName, Mime: u.ContentType})
	if err != nil {
		return File{}, err
	}
	return File{Upload: u, URL: signed.URL, ThumbnailURL: signed.ThumbnailURL}, nil
}

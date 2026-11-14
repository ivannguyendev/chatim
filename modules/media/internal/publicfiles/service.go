package publicfiles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
)

const MaxKeys = 100

type Uploads interface {
	GetMany(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error)
}

type Config struct {
	SignatureTTL time.Duration
	Thumbnails   domain.Thumbnails
}

type Deps struct {
	Uploads Uploads
	Objects download.Objects
	Ready   func() bool
	Now     func() time.Time
}

type Result struct {
	Files     []download.FileURL
	ExpiresAt time.Time
}

type Service struct {
	cfg    Config
	deps   Deps
	signer download.Signer
}

func New(cfg Config, deps Deps) (*Service, error) {
	if deps.Uploads == nil || deps.Objects == nil || cfg.SignatureTTL <= 0 {
		return nil, errors.New("publicfiles: needs uploads, objects and a positive signature lifetime")
	}
	if deps.Ready == nil {
		deps.Ready = func() bool { return true }
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{cfg: cfg, deps: deps, signer: download.Signer{Objects: deps.Objects, TTL: cfg.SignatureTTL, Thumbnails: cfg.Thumbnails}}, nil
}

func (s *Service) URLs(ctx context.Context, who domain.Caller, keys []string) (Result, error) {
	if !s.deps.Ready() {
		return Result{}, domain.ErrNotReady
	}
	wanted, err := publicKeysOf(who.Tenant, keys)
	if err != nil {
		return Result{}, err
	}
	out := Result{Files: make([]download.FileURL, 0, len(wanted)), ExpiresAt: s.deps.Now().UTC().Add(s.cfg.SignatureTTL)}
	if len(wanted) == 0 {
		return out, nil
	}
	docs, err := s.deps.Uploads.GetMany(ctx, who.Tenant, wanted)
	if err != nil {
		return Result{}, err
	}
	for _, k := range wanted {
		u, ok := docs[k]
		if !ok || u.Tenant != who.Tenant || u.State != domain.StateUploaded {
			continue
		}
		f, err := s.signer.Sign(ctx, u.Bucket, domain.File{Key: k, Mime: u.ContentType})
		if err != nil {
			return Result{}, err
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

func publicKeysOf(tenant string, keys []string) ([]string, error) {
	if len(keys) == 0 || len(keys) > MaxKeys {
		return nil, fmt.Errorf("%w: ask for 1..%d keys", domain.ErrInvalid, MaxKeys)
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		info, ok := domain.ParseKey(k)
		if !ok {
			return nil, fmt.Errorf("%w: keys must be media file keys", domain.ErrInvalid)
		}
		if _, dup := seen[k]; dup || !info.Public || info.Tenant != tenant {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, nil
}

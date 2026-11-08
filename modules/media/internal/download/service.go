package download

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

const (
	MaxMessages = 100
	UploadBatch = 200
)

type Chat interface {
	VisibleAttachments(ctx context.Context, tenant, user string, refs []domain.MessageRef) ([]domain.MessageFiles, error)
}

type Uploads interface {
	GetMany(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error)
}

type Objects interface {
	SignGet(ctx context.Context, bucket domain.Bucket, key string, ttl time.Duration) (string, error)
}

type Config struct {
	SignatureTTL time.Duration
	Thumbnails   domain.Thumbnails
}

type Deps struct {
	Chat    Chat
	Uploads Uploads
	Objects Objects
	Ready   func() bool
	Now     func() time.Time
}

type FileURL struct {
	Key          string
	URL          string
	ThumbnailURL string
}

type MessageURLs struct {
	Ref   domain.MessageRef
	Files []FileURL
}

type Result struct {
	Messages  []MessageURLs
	ExpiresAt time.Time
}

type Service struct {
	cfg    Config
	deps   Deps
	signer Signer
}

type Signer struct {
	Objects    Objects
	TTL        time.Duration
	Thumbnails domain.Thumbnails
}

func New(cfg Config, deps Deps) (*Service, error) {
	if deps.Chat == nil || deps.Uploads == nil || deps.Objects == nil || cfg.SignatureTTL <= 0 {
		return nil, errors.New("download: needs chat, uploads, objects and a positive signature lifetime")
	}
	if deps.Ready == nil {
		deps.Ready = func() bool { return true }
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{cfg: cfg, deps: deps, signer: Signer{Objects: deps.Objects, TTL: cfg.SignatureTTL, Thumbnails: cfg.Thumbnails}}, nil
}

func (s *Service) AttachmentURLs(ctx context.Context, who domain.Caller, refs []domain.MessageRef) (Result, error) {
	if !s.deps.Ready() {
		return Result{}, domain.ErrNotReady
	}
	if len(refs) == 0 || len(refs) > MaxMessages {
		return Result{}, fmt.Errorf("%w: ask for 1..%d messages", domain.ErrInvalid, MaxMessages)
	}
	found, err := s.deps.Chat.VisibleAttachments(ctx, who.Tenant, who.User, unique(refs))
	if err != nil {
		return Result{}, err
	}
	buckets, err := s.bucketsOf(ctx, who.Tenant, found)
	if err != nil {
		return Result{}, err
	}
	out := Result{Messages: make([]MessageURLs, 0, len(found)), ExpiresAt: s.deps.Now().UTC().Add(s.cfg.SignatureTTL)}
	for _, m := range found {
		files := make([]FileURL, 0, len(m.Files))
		for _, f := range m.Files {
			u, err := s.signer.Sign(ctx, buckets[f.Key], f)
			if err != nil {
				return Result{}, err
			}
			files = append(files, u)
		}
		out.Messages = append(out.Messages, MessageURLs{Ref: m.Ref, Files: files})
	}
	return out, nil
}

func (s *Service) bucketsOf(ctx context.Context, tenant string, found []domain.MessageFiles) (map[string]domain.Bucket, error) {
	var keys []string
	seen := make(map[string]struct{})
	for _, m := range found {
		for _, f := range m.Files {
			if _, dup := seen[f.Key]; !dup {
				seen[f.Key] = struct{}{}
				keys = append(keys, f.Key)
			}
		}
	}
	out := make(map[string]domain.Bucket, len(keys))
	for batch := range slices.Chunk(keys, UploadBatch) {
		docs, err := s.deps.Uploads.GetMany(ctx, tenant, batch)
		if err != nil {
			return nil, err
		}
		for _, k := range batch {
			if u, ok := docs[k]; ok && u.Tenant == tenant {
				out[k] = u.Bucket
			}
		}
	}
	return out, nil
}

func (s Signer) Sign(ctx context.Context, bucket domain.Bucket, f domain.File) (FileURL, error) {
	bucket = bucket.OrProcessing()
	u, err := s.Objects.SignGet(ctx, bucket, f.Key, s.TTL)
	if err != nil {
		return FileURL{}, fmt.Errorf("%w: %w", domain.ErrRetryLater, err)
	}
	out := FileURL{Key: f.Key, URL: u}
	if thumb, ok := s.Thumbnails.KeyFor(f.Key, f.Mime); ok {
		if out.ThumbnailURL, err = s.Objects.SignGet(ctx, bucket, thumb, s.TTL); err != nil {
			return FileURL{}, fmt.Errorf("%w: %w", domain.ErrRetryLater, err)
		}
	}
	return out, nil
}

func unique(refs []domain.MessageRef) []domain.MessageRef {
	seen := make(map[domain.MessageRef]struct{}, len(refs))
	out := make([]domain.MessageRef, 0, len(refs))
	for _, r := range refs {
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

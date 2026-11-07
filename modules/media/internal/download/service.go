package download

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

const MaxMessages = 100

type Chat interface {
	VisibleAttachments(ctx context.Context, tenant, user string, refs []domain.MessageRef) ([]domain.MessageFiles, error)
}

type Objects interface {
	SignGet(ctx context.Context, key string, ttl time.Duration, d domain.Disposition) (string, error)
}

type Config struct {
	SignatureTTL time.Duration
	Thumbnails   domain.Thumbnails
}

type Deps struct {
	Chat    Chat
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
	if deps.Chat == nil || deps.Objects == nil || cfg.SignatureTTL <= 0 {
		return nil, errors.New("download: needs chat, objects and a positive signature lifetime")
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
	out := Result{Messages: make([]MessageURLs, 0, len(found)), ExpiresAt: s.deps.Now().UTC().Add(s.cfg.SignatureTTL)}
	for _, m := range found {
		files := make([]FileURL, 0, len(m.Files))
		for _, f := range m.Files {
			u, err := s.signer.Sign(ctx, f)
			if err != nil {
				return Result{}, err
			}
			files = append(files, u)
		}
		out.Messages = append(out.Messages, MessageURLs{Ref: m.Ref, Files: files})
	}
	return out, nil
}

func (s Signer) Sign(ctx context.Context, f domain.File) (FileURL, error) {
	u, err := s.Objects.SignGet(ctx, f.Key, s.TTL, domain.FileDisposition(f.Name, f.Mime))
	if err != nil {
		return FileURL{}, fmt.Errorf("%w: %w", domain.ErrRetryLater, err)
	}
	out := FileURL{Key: f.Key, URL: u}
	if thumb, ok := s.Thumbnails.KeyFor(f.Key, f.Mime); ok {
		if out.ThumbnailURL, err = s.Objects.SignGet(ctx, thumb, s.TTL, domain.ThumbnailDisposition()); err != nil {
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

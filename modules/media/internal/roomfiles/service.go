package roomfiles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
)

const MaxPageSize = 100

type Chat interface {
	RoomReadAccess(ctx context.Context, tenant, user, room string) (domain.RoomAccess, error)
	VisibleAttachments(ctx context.Context, tenant, user string, refs []domain.MessageRef) ([]domain.MessageFiles, error)
}

type Files interface {
	ListRoom(ctx context.Context, q domain.RoomFilesQuery) ([]domain.MessageFile, error)
}

type Config struct {
	SignatureTTL time.Duration
	Thumbnails   domain.Thumbnails
}

type Deps struct {
	Chat    Chat
	Files   Files
	Uploads download.Uploads
	Objects download.Objects
	Ready   func() bool
	Now     func() time.Time
}

type Input struct {
	Room     string
	Kind     domain.RoomFileKind
	Before   string
	PageSize int
}

type File struct {
	Ref          domain.MessageRef
	Sender       string
	SentAt       time.Time
	File         domain.File
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
	if deps.Chat == nil || deps.Files == nil || deps.Uploads == nil || deps.Objects == nil || cfg.SignatureTTL <= 0 {
		return nil, errors.New("roomfiles: needs chat, files, uploads, objects and a positive signature lifetime")
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
	before, err := checkInput(in)
	if err != nil {
		return Page{}, err
	}
	access, err := s.deps.Chat.RoomReadAccess(ctx, who.Tenant, who.User, in.Room)
	if err != nil {
		return Page{}, err
	}
	page := Page{Files: []File{}, ExpiresAt: s.deps.Now().UTC().Add(s.cfg.SignatureTTL)}
	if !before.IsZero() && !before.SentAt.After(access.ClearedAt) {
		return page, nil
	}
	pairs, err := s.deps.Files.ListRoom(ctx, domain.RoomFilesQuery{
		Tenant: who.Tenant, Room: access.Room, Kind: in.Kind, ClearedAt: access.ClearedAt, Before: before, Limit: in.PageSize,
	})
	if err != nil {
		return Page{}, err
	}
	if len(pairs) == in.PageSize {
		page.NextBefore = domain.CursorOf(pairs[len(pairs)-1]).Encode()
	}
	if len(pairs) == 0 {
		return page, nil
	}
	if page.Files, err = s.files(ctx, who, pairs); err != nil {
		return Page{}, err
	}
	return page, nil
}

func checkInput(in Input) (domain.RoomFileCursor, error) {
	switch {
	case in.PageSize < 1 || in.PageSize > MaxPageSize:
		return domain.RoomFileCursor{}, fmt.Errorf("%w: page size must be 1..%d", domain.ErrInvalid, MaxPageSize)
	case !in.Kind.Valid():
		return domain.RoomFileCursor{}, fmt.Errorf("%w: unknown room file kind %q", domain.ErrInvalid, in.Kind)
	}
	return domain.ParseRoomFileCursor(in.Before)
}

func (s *Service) files(ctx context.Context, who domain.Caller, pairs []domain.MessageFile) ([]File, error) {
	var refs []domain.MessageRef
	seen := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		ref, err := domain.ParseMessageKey(p.MessageKey)
		if _, dup := seen[p.MessageKey]; err != nil || dup {
			continue
		}
		seen[p.MessageKey] = struct{}{}
		refs = append(refs, ref)
	}
	visible, err := s.deps.Chat.VisibleAttachments(ctx, who.Tenant, who.User, refs)
	if err != nil {
		return nil, err
	}
	shown := make(map[string]domain.MessageFiles, len(visible))
	for _, m := range visible {
		shown[m.Ref.Key()] = m
	}
	kept := keptFiles(pairs, shown)
	keys := make([]string, len(kept))
	for i, k := range kept {
		keys[i] = k.File.Key
	}
	buckets, err := download.Buckets(ctx, s.deps.Uploads, who.Tenant, keys)
	if err != nil {
		return nil, err
	}
	for i, k := range kept {
		signed, err := s.signer.Sign(ctx, buckets[k.File.Key], k.File)
		if err != nil {
			return nil, err
		}
		kept[i].URL, kept[i].ThumbnailURL = signed.URL, signed.ThumbnailURL
	}
	return kept, nil
}

func keptFiles(pairs []domain.MessageFile, shown map[string]domain.MessageFiles) []File {
	out := make([]File, 0, len(pairs))
	for _, p := range pairs {
		m, ok := shown[p.MessageKey]
		if !ok {
			continue
		}
		for _, f := range m.Files {
			if f.Key == p.FileKey {
				out = append(out, File{Ref: m.Ref, Sender: m.Sender, SentAt: m.SentAt, File: f})
				break
			}
		}
	}
	return out
}

package message

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/message/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errMissingReaderDeps = fmt.Errorf("%w: message reader needs access, pages, edits, hidden and interactions", apperr.ErrInvalidArgument)

type Pages interface {
	Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error)
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type HiddenSeqs interface {
	HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error)
}

type MyInteractions interface {
	MyInteractions(ctx context.Context, user string, keys []store.MsgKey) (map[store.MsgKey]store.MyInteraction, error)
}

type ReaderDeps struct {
	Access       *access.Checker
	Pages        Pages
	Edits        store.Edits
	Hidden       HiddenSeqs
	Interactions MyInteractions
}

type Item struct {
	Message      domain.Message
	ReplyPreview *domain.Message
	MyReaction   string
	Bookmarked   bool
}

type Reader struct {
	d        ReaderDeps
	view     view.Pipeline
	maskView view.Pipeline
}

func NewReader(d ReaderDeps) (*Reader, error) {
	if d.Access == nil || d.Pages == nil || d.Edits == nil || d.Hidden == nil || d.Interactions == nil {
		return nil, errMissingReaderDeps
	}
	return &Reader{d: d, view: view.Default(), maskView: view.New(view.MaskDeleted, view.HideForViewer)}, nil
}

func (r *Reader) Masked(ctx context.Context, user string, grant access.Request, room, thread uint64, found []domain.Message) ([]domain.Message, error) {
	viewer, err := r.viewerOf(ctx, user, grant, room, thread, found)
	if err != nil {
		return nil, err
	}
	return r.maskView.Apply(viewer, found), nil
}

func (r *Reader) Viewed(ctx context.Context, user string, grant access.Request, room, thread uint64, found []domain.Message) ([]domain.Message, error) {
	viewer, err := r.viewerOf(ctx, user, grant, room, thread, found)
	if err != nil {
		return nil, err
	}
	return r.view.Apply(viewer, found), nil
}

func (r *Reader) viewerOf(ctx context.Context, user string, grant access.Request, room, thread uint64, page []domain.Message, quoted ...uint64) (view.Viewer, error) {
	v := view.Viewer{User: user, Room: grant.Room, ClearedAt: grant.Member.ClearedAt}
	if len(page) == 0 {
		return v, nil
	}
	lo, hi, newest := page[0].Seq, page[0].Seq, page[0].CreatedAt
	for _, m := range page[1:] {
		lo, hi = min(lo, m.Seq), max(hi, m.Seq)
		if m.CreatedAt.After(newest) {
			newest = m.CreatedAt
		}
	}
	for _, seq := range quoted {
		lo = min(lo, seq)
	}
	if v.Cleared(newest) {
		return v, nil
	}
	seqs, err := r.d.Hidden.HiddenIn(ctx, user, room, thread, lo, hi)
	if err != nil {
		return view.Viewer{}, err
	}
	v.HiddenSeqs = make(map[uint64]bool, len(seqs))
	for _, seq := range seqs {
		v.HiddenSeqs[seq] = true
	}
	return v, nil
}

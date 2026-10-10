package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func quotedKey(room uint64, m domain.Message) (store.MsgKey, bool) {
	if m.ReplyTo == nil || m.Deleted || m.Hidden {
		return store.MsgKey{}, false
	}
	return store.MsgKey{Room: room, Thread: m.ReplyTo.Thread, Seq: m.ReplyTo.Seq}, true
}

func quotedSeqs(room uint64, page []domain.Message) []uint64 {
	var seqs []uint64
	for _, m := range page {
		if k, ok := quotedKey(room, m); ok {
			seqs = append(seqs, k.Seq)
		}
	}
	return seqs
}

func (s *Service) addReplyPreviews(ctx context.Context, viewer view.Viewer, room uint64, page []domain.Message, out []*chatimv1.Message) error {
	known := make(map[store.MsgKey]domain.Message, len(page))
	for _, m := range page {
		known[store.KeyOf(m)] = m
	}
	var missing []store.MsgKey
	asked := make(map[store.MsgKey]bool)
	for _, m := range page {
		k, ok := quotedKey(room, m)
		if !ok || asked[k] {
			continue
		}
		asked[k] = true
		if _, ok := known[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		found, err := s.pages.Find(ctx, room, missing)
		if err != nil {
			return err
		}
		for _, p := range s.bookmarkView.Apply(viewer, found) {
			known[store.KeyOf(p)] = p
		}
	}
	for i, m := range page {
		if k, ok := quotedKey(room, m); ok {
			if p, ok := known[k]; ok {
				out[i].ReplyPreview = pbconv.ReplyPreview(p)
			}
		}
	}
	return nil
}

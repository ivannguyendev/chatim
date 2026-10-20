package message

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/message/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func VisibleToReader(m domain.Message) bool { return !m.Deleted && !m.Hidden }

func (r *Reader) Mine(ctx context.Context, user string, page []domain.Message) ([]Item, error) {
	out := itemsOf(page)
	if err := r.addMine(ctx, user, page, out); err != nil {
		return nil, err
	}
	return out, nil
}

func itemsOf(page []domain.Message) []Item {
	out := make([]Item, len(page))
	for i, m := range page {
		out[i] = Item{Message: m}
	}
	return out
}

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

func (r *Reader) addReplyPreviews(ctx context.Context, viewer view.Viewer, room uint64, page []domain.Message, out []Item) error {
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
		found, err := r.d.Pages.Find(ctx, room, missing)
		if err != nil {
			return err
		}
		for _, p := range r.maskView.Apply(viewer, found) {
			known[store.KeyOf(p)] = p
		}
	}
	for i, m := range page {
		if k, ok := quotedKey(room, m); ok {
			if p, ok := known[k]; ok {
				out[i].ReplyPreview = &p
			}
		}
	}
	return nil
}

func (r *Reader) addMine(ctx context.Context, user string, page []domain.Message, out []Item) error {
	var asked []store.MsgKey
	for _, m := range page {
		if VisibleToReader(m) {
			asked = append(asked, store.KeyOf(m))
		}
	}
	if len(asked) == 0 {
		return nil
	}
	mine, err := r.d.Interactions.MyInteractions(ctx, user, asked)
	if err != nil {
		return err
	}
	for i, m := range page {
		if x, ok := mine[store.KeyOf(m)]; ok && VisibleToReader(m) {
			out[i].MyReaction, out[i].Bookmarked = x.Reaction, x.Bookmarked
		}
	}
	return nil
}

package view_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/view"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func withLinks(seq uint64, deleted bool) domain.Message {
	return domain.Message{
		Room: 7, Seq: seq, From: "alice", Text: "t", CID: "c", Version: 2, Deleted: deleted,
		CreatedAt:  editedAt.Add(time.Duration(seq) * time.Second),
		ReplyTo:    &domain.ReplyRef{Seq: 1},
		Forward:    &domain.ForwardRef{Room: 555, Seq: 9, Author: "lan", SentAt: editedAt},
		Mentions:   []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}},
		MentionAll: true,
		Replies:    domain.ReplyCount{N: 2, Version: 2},
	}
}

func TestMaskDeletedDropsContentButKeepsTheReplyLink(t *testing.T) {
	page := []domain.Message{withLinks(1, false), withLinks(2, true)}
	got := view.MaskDeleted(view.Viewer{}, page)
	if !reflect.DeepEqual(got[0], page[0]) {
		t.Fatalf("live message changed: %+v", got[0])
	}
	want := page[1]
	want.Text, want.Forward, want.Mentions, want.MentionAll, want.Replies = "", nil, nil, false, domain.ReplyCount{}
	if !reflect.DeepEqual(got[1], want) {
		t.Fatalf("deleted = %+v, want %+v", got[1], want)
	}
	if page[1].Mentions == nil || page[1].Forward == nil {
		t.Fatal("input page was modified")
	}
}

func TestHideForViewerDropsEveryLink(t *testing.T) {
	page := []domain.Message{withLinks(1, false), withLinks(2, false)}
	got := view.HideForViewer(view.Viewer{HiddenSeqs: map[uint64]bool{2: true}}, page)
	if !reflect.DeepEqual(got[0], page[0]) {
		t.Fatalf("visible message changed: %+v", got[0])
	}
	want := domain.Message{Room: 7, Seq: 2, From: "alice", CID: "c", Version: 2, CreatedAt: page[1].CreatedAt, Hidden: true}
	if !reflect.DeepEqual(got[1], want) {
		t.Fatalf("hidden = %+v, want %+v", got[1], want)
	}
}

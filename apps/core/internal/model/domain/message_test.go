package domain_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestParseKindMapsConfigNamesToKinds(t *testing.T) {
	got, err := domain.ParseKind("text")
	if err != nil || got != domain.KindText {
		t.Fatalf(`ParseKind("text") = %d, %v; want KindText`, got, err)
	}
	for _, name := range []string{"", "system", "Text"} {
		if got, err := domain.ParseKind(name); !errors.Is(err, apperr.ErrInvalidArgument) || got != 0 {
			t.Fatalf("ParseKind(%q) = %d, %v; want 0 and ErrInvalidArgument", name, got, err)
		}
	}
}

func TestWithoutDeletedContentKeepsOnlyTheReplyLink(t *testing.T) {
	m := domain.Message{
		Room: 7, Seq: 2, From: "alice", Text: "t", Version: 3,
		ReplyTo:    &domain.ReplyRef{Seq: 1},
		Forward:    &domain.ForwardRef{Room: 8, Seq: 9},
		Mentions:   []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}},
		MentionAll: true,
		Reactions:  domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 1},
		Replies:    domain.ReplyCount{N: 1, Version: 1},
	}
	if got := m.WithoutDeletedContent(); !reflect.DeepEqual(got, m) {
		t.Fatalf("live message changed: %+v", got)
	}
	m.Deleted = true
	want := domain.Message{Room: 7, Seq: 2, From: "alice", Version: 3, Deleted: true, ReplyTo: &domain.ReplyRef{Seq: 1}}
	if got := m.WithoutDeletedContent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted = %+v, want %+v", got, want)
	}
}

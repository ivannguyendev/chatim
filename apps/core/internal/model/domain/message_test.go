package domain_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestParseKindMapsConfigNamesToKinds(t *testing.T) {
	for name, want := range map[string]domain.Kind{"text": domain.KindText, "system": domain.KindSystem} {
		if got, err := domain.ParseKind(name); err != nil || got != want {
			t.Fatalf("ParseKind(%q) = %d, %v; want %d", name, got, err, want)
		}
	}
	for _, name := range []string{"", "System", "Text", "sys"} {
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
		SkipUnread: true,
		Meta:       []byte(`{"call_seconds":180}`),
	}
	if got := m.WithoutDeletedContent(); !reflect.DeepEqual(got, m) {
		t.Fatalf("live message changed: %+v", got)
	}
	m.Deleted = true
	want := domain.Message{Room: 7, Seq: 2, From: "alice", Version: 3, Deleted: true, ReplyTo: &domain.ReplyRef{Seq: 1}, SkipUnread: true}
	if got := m.WithoutDeletedContent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted = %+v, want %+v", got, want)
	}
}

func TestValidateSystemMessageRefusesLinks(t *testing.T) {
	sys := func(edit func(*domain.Message)) domain.Message {
		m := domain.Message{Kind: domain.KindSystem, Text: "Minh pinned a message", Meta: []byte(`{}`), SkipUnread: true}
		edit(&m)
		return m
	}
	if err := domain.ValidateSystemMessage(sys(func(*domain.Message) {})); err != nil {
		t.Fatalf("plain system message = %v, want nil", err)
	}
	cases := map[string]struct {
		edit  func(*domain.Message)
		field string
	}{
		"reply":       {func(m *domain.Message) { m.ReplyTo = &domain.ReplyRef{Seq: 1} }, "reply_to"},
		"forward":     {func(m *domain.Message) { m.Forward = &domain.ForwardRef{Room: 8, Seq: 9} }, "forward_from"},
		"mention":     {func(m *domain.Message) { m.Mentions = []domain.MentionTarget{{Kind: domain.MentionUser, ID: "lan"}} }, "mentions"},
		"mention all": {func(m *domain.Message) { m.MentionAll = true }, "mentions"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assertInvalid(t, domain.ValidateSystemMessage(sys(c.edit)), c.field)
			text := sys(c.edit)
			text.Kind = domain.KindText
			if err := domain.ValidateSystemMessage(text); err != nil {
				t.Fatalf("text message with %s = %v, want nil", name, err)
			}
		})
	}
}

package domain_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func user(id string) domain.MentionTarget {
	return domain.MentionTarget{Kind: domain.MentionUser, ID: id}
}
func group(id string) domain.MentionTarget {
	return domain.MentionTarget{Kind: domain.MentionGroup, ID: id}
}

func TestValidateMentionsDropsDuplicatesAndKeepsOrder(t *testing.T) {
	in := []domain.MentionTarget{user("minh"), group("team-design"), user("minh"), user("team-design"), group("team-design"), user("lan")}
	got, err := domain.ValidateMentions(in, domain.DefaultMentionTargetsMax)
	want := []domain.MentionTarget{user("minh"), group("team-design"), user("team-design"), user("lan")}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("ValidateMentions = %v %v, want %v", got, err, want)
	}
	if got, err := domain.ValidateMentions(nil, domain.DefaultMentionTargetsMax); err != nil || got != nil {
		t.Fatalf("ValidateMentions(nil) = %v %v, want nil nil", got, err)
	}
}

func TestValidateMentionsCountsTargetsAfterDropping(t *testing.T) {
	if domain.DefaultMentionTargetsMax != 50 {
		t.Fatalf("DefaultMentionTargetsMax = %d, want 50", domain.DefaultMentionTargetsMax)
	}
	targets := make([]domain.MentionTarget, 0, 52)
	for i := range 50 {
		targets = append(targets, user(fmt.Sprintf("u%d", i)))
	}
	targets = append(targets, user("u0"), group("u1"))
	if got, err := domain.ValidateMentions(targets[:51], 50); err != nil || len(got) != 50 {
		t.Fatalf("50 targets plus a duplicate = %d targets, %v; want 50, nil", len(got), err)
	}
	_, err := domain.ValidateMentions(targets, 50)
	assertInvalid(t, err, "mentions")
	if _, err := domain.ValidateMentions(targets[:3], 2); err == nil {
		t.Fatal("3 targets with max 2 must be rejected")
	}
}

func TestValidateMentionsChecksKindAndID(t *testing.T) {
	for _, id := range []string{"a", "team:design", "A-Z_0-9", strings.Repeat("x", 64)} {
		if _, err := domain.ValidateMentions([]domain.MentionTarget{group(id)}, 50); err != nil {
			t.Errorf("id %q rejected: %v", id, err)
		}
	}
	for _, id := range []string{"", strings.Repeat("x", 65), "a b", "a.b", "@minh", "chào", "a/b"} {
		_, err := domain.ValidateMentions([]domain.MentionTarget{user(id)}, 50)
		assertInvalid(t, err, "mentions")
	}
	for _, kind := range []domain.MentionKind{0, 3, 9} {
		_, err := domain.ValidateMentions([]domain.MentionTarget{{Kind: kind, ID: "minh"}}, 50)
		assertInvalid(t, err, "mentions")
	}
}

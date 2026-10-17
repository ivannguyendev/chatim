package domain_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestAllIsNotASendableMentionTarget(t *testing.T) {
	if _, err := domain.ValidateMentions([]domain.MentionTarget{{Kind: domain.MentionAll}}, 5); err == nil {
		t.Fatal("ValidateMentions accepted an @all target; @all travels as its own flag")
	}
}

func TestMentionLinksTakeAllOnlyWithoutAnID(t *testing.T) {
	cases := map[string]struct {
		target domain.MentionTarget
		want   bool
	}{
		"user":        {domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}, true},
		"group":       {domain.MentionTarget{Kind: domain.MentionGroup, ID: "team-design"}, true},
		"all":         {domain.MentionTarget{Kind: domain.MentionAll}, true},
		"all with id": {domain.MentionTarget{Kind: domain.MentionAll, ID: "x"}, false},
		"user no id":  {domain.MentionTarget{Kind: domain.MentionUser}, false},
	}
	for name, c := range cases {
		if got := domain.ValidMentionLink(c.target); got != c.want {
			t.Errorf("%s: ValidMentionLink = %v, want %v", name, got, c.want)
		}
	}
}

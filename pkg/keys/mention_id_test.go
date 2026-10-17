package keys

import (
	"strings"
	"testing"
)

func TestValidMentionIDAllowsLettersDigitsUnderscoreDashAndColon(t *testing.T) {
	cases := []struct {
		id string
		ok bool
	}{
		{"alice", true},
		{"Team_Design-01", true},
		{"group:eng", true},
		{strings.Repeat("Z", MaxMentionID), true},
		{"", false},
		{strings.Repeat("Z", MaxMentionID+1), false},
		{"al.ice", false},
		{"al ice", false},
		{"a@b", false},
		{"người", false},
		{"a\x00", false},
	}
	for _, c := range cases {
		if got := ValidMentionID(c.id); got != c.ok {
			t.Errorf("ValidMentionID(%q) = %v, want %v", c.id, got, c.ok)
		}
	}
}

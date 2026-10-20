package message_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestLimitsFillDefaultsAndCheckBounds(t *testing.T) {
	cases := []struct {
		name   string
		limits message.Limits
		bad    string
	}{
		{"defaults", message.Limits{}, ""},
		{"one target", message.Limits{MentionTargets: 1}, ""},
		{"negative mention targets", message.Limits{MentionTargets: -2}, "-2"},
	}
	for _, c := range cases {
		err := c.limits.Validate()
		switch {
		case c.bad == "" && err != nil:
			t.Fatalf("%s: Validate() = %v, want nil", c.name, err)
		case c.bad != "" && (!errors.Is(err, apperr.ErrInvalidArgument) || !strings.Contains(err.Error(), c.bad)):
			t.Fatalf("%s: Validate() = %v, want ErrInvalidArgument naming %q", c.name, err, c.bad)
		}
	}
}

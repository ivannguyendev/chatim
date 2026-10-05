package access_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
)

func TestDefaultPolicyLetsOnlyTheAuthorEditOrDelete(t *testing.T) {
	cases := []struct {
		action       access.Action
		user, author string
		want         error
	}{
		{access.EditMessage, "alice", "alice", nil},
		{access.DeleteMessage, "alice", "alice", nil},
		{access.EditMessage, "alice", "bob", access.ErrDenied},
		{access.DeleteMessage, "alice", "bob", access.ErrDenied},
		{access.DeleteMessage, "alice", "", access.ErrDenied},
		{access.ReadHistory, "alice", "", nil},
		{access.SendMessage, "alice", "", nil},
		{access.ClearHistory, "alice", "", nil},
		{access.HideMessage, "alice", "bob", nil},
		{access.ReadEditHistory, "alice", "bob", nil},
	}
	for _, tc := range cases {
		err := access.DefaultPolicy{}.Check(t.Context(), access.Request{Action: tc.action, User: tc.user, Author: tc.author})
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s by %s on %q's message = %v, want %v", tc.action, tc.user, tc.author, err, tc.want)
		}
	}
}

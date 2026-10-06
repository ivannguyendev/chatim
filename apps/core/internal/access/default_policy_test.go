package access_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
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

func TestDefaultPolicyLocksEditAndDeleteOfConfiguredKinds(t *testing.T) {
	locked := access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}}
	cases := []struct {
		name         string
		policy       access.DefaultPolicy
		action       access.Action
		user, author string
		want         error
	}{
		{"author edit of a locked kind", locked, access.EditMessage, "alice", "alice", access.ErrDenied},
		{"author delete of a locked kind", locked, access.DeleteMessage, "alice", "alice", access.ErrDenied},
		{"hide of a locked kind", locked, access.HideMessage, "alice", "bob", nil},
		{"read history on a locked kind", locked, access.ReadHistory, "alice", "", nil},
		{"send on a locked kind", locked, access.SendMessage, "alice", "", nil},
		{"author edit with nothing locked", access.DefaultPolicy{}, access.EditMessage, "alice", "alice", nil},
		{"other edit with nothing locked", access.DefaultPolicy{}, access.EditMessage, "alice", "bob", access.ErrDenied},
	}
	for _, tc := range cases {
		req := access.Request{Action: tc.action, User: tc.user, Author: tc.author, Kind: domain.KindText}
		if err := tc.policy.Check(t.Context(), req); !errors.Is(err, tc.want) {
			t.Fatalf("%s = %v, want %v", tc.name, err, tc.want)
		}
	}
}

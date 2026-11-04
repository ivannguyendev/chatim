package mongostore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func mustCreateChain(t *testing.T, s *Store, tenantID string, ids ...string) {
	t.Helper()
	parent := ""
	for _, id := range ids {
		mustCreateGroup(t, s, tenantID, id, parent)
		parent = id
	}
}

func moveGroup(t *testing.T, s *Store, tenantID, id, parent string) error {
	t.Helper()
	return s.UpdateGroup(t.Context(), tenantID, id, store.GroupUpdate{ParentID: &parent}, itAt.Add(time.Minute), "cli:ops")
}

func expectAncestors(t *testing.T, s *Store, tenantID string, want map[string][]string) {
	t.Helper()
	for id, ancestors := range want {
		if got := readGroupDoc(t, s, tenantID, id); !slices.Equal(got.Ancestors, ancestors) {
			t.Fatalf("ancestors of %s = %v; want %v", id, got.Ancestors, ancestors)
		}
	}
}

func TestUpdateGroupMovesASubtreeAndKeepsTheSubtreeVersions(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateChain(t, s, "shop", "a", "b", "c", "d")
	expectAncestors(t, s, "shop", map[string][]string{"b": {"a"}, "c": {"a", "b"}, "d": {"a", "b", "c"}})
	if err := moveGroup(t, s, "shop", "b", ""); err != nil {
		t.Fatalf("move b to the root: %v", err)
	}
	moved := readGroupDoc(t, s, "shop", "b")
	if moved.ParentID != "" || len(moved.Ancestors) != 0 || moved.Ver != 2 || !moved.UpdatedAt.Equal(itAt.Add(time.Minute)) || moved.UpdatedBy != "cli:ops" {
		t.Fatalf("moved group = %+v; want a root, ver 2, updated by cli:ops", moved)
	}
	expectAncestors(t, s, "shop", map[string][]string{"a": {}, "c": {"b"}, "d": {"b", "c"}})
	for _, id := range []string{"a", "c", "d"} {
		if got := readGroupDoc(t, s, "shop", id); got.Ver != 1 || !got.UpdatedAt.Equal(itAt) || got.UpdatedBy != "cli:admin" {
			t.Fatalf("group %s = %+v; want ver 1 and updated_* kept", id, got)
		}
	}
	mustCreateGroup(t, s, "shop", "e", "")
	if err := moveGroup(t, s, "shop", "b", "e"); err != nil {
		t.Fatalf("move b under e: %v", err)
	}
	expectAncestors(t, s, "shop", map[string][]string{"b": {"e"}, "c": {"e", "b"}, "d": {"e", "b", "c"}})
	if err := moveGroup(t, s, "shop", "c", "a"); err != nil {
		t.Fatalf("move c under a: %v", err)
	}
	expectAncestors(t, s, "shop", map[string][]string{"b": {"e"}, "c": {"a"}, "d": {"a", "c"}})
	if tenant := readTenant(t, s, "shop"); tenant.GroupTreeVer != 8 || tenant.Ver != 1 {
		t.Fatalf("tenant = %+v; want group_tree_ver 8 (5 creates, 3 moves) and ver 1 kept", tenant)
	}
}

func TestUpdateGroupToTheSameParentKeepsTheVersionAndCountsVisibleChangesOnce(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateChain(t, s, "shop", "a", "b")
	mustCreateGroup(t, s, "shop", "z", "")
	if err := moveGroup(t, s, "shop", "b", "a"); err != nil {
		t.Fatalf("move b under its own parent: %v", err)
	}
	if got := readGroupDoc(t, s, "shop", "b"); got.Ver != 1 || !got.UpdatedAt.Equal(itAt) || !slices.Equal(got.Ancestors, []string{"a"}) {
		t.Fatalf("group b = %+v; want ver 1, updated_at kept, ancestors [a]", got)
	}
	parent, name, hidden := "z", "Renamed", false
	u := store.GroupUpdate{ParentID: &parent, Name: &name, Mentionable: &hidden}
	if err := s.UpdateGroup(t.Context(), "shop", "b", u, itAt.Add(time.Hour), "cli:ops"); err != nil {
		t.Fatalf("move and rename: %v", err)
	}
	got := readGroupDoc(t, s, "shop", "b")
	if got.ParentID != "z" || !slices.Equal(got.Ancestors, []string{"z"}) || got.Name != name || got.Mentionable || got.Ver != 2 {
		t.Fatalf("group b = %+v; want parent z, renamed, hidden, ver 2", got)
	}
}

func TestUpdateGroupRefusesACycle(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateChain(t, s, "shop", "a", "b", "c", "d")
	for _, c := range []struct{ id, parent string }{{"a", "d"}, {"a", "a"}, {"b", "c"}} {
		err := moveGroup(t, s, "shop", c.id, c.parent)
		if !errors.Is(err, apperr.ErrFailedPrecondition) || err.Error() != errGroupCycle.Error() {
			t.Fatalf("move %s under %s = %v; want %v", c.id, c.parent, err, errGroupCycle)
		}
	}
	expectAncestors(t, s, "shop", map[string][]string{"a": {}, "b": {"a"}, "c": {"a", "b"}, "d": {"a", "b", "c"}})
	if tenant := readTenant(t, s, "shop"); tenant.GroupTreeVer != 4 {
		t.Fatalf("group_tree_ver = %d; want 4 after refused moves", tenant.GroupTreeVer)
	}
}

func TestUpdateGroupUnderAnUnknownOrDeletedParentIsNotFound(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "a", "")
	insertDeletedGroup(t, s, "shop", "gone")
	for _, parent := range []string{"ghost", "gone"} {
		if err := moveGroup(t, s, "shop", "a", parent); !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("move a under %s = %v; want not found", parent, err)
		}
	}
	if got := readGroupDoc(t, s, "shop", "a"); got.ParentID != "" || got.Ver != 1 {
		t.Fatalf("group a = %+v; want it unchanged", got)
	}
	if err := moveGroup(t, s, "ghost", "a", ""); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("move in an unknown tenant = %v; want not found", err)
	}
}

func TestUpdateGroupConcurrentMovesLeaveNoCycle(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "a", "")
	mustCreateGroup(t, s, "shop", "b", "")
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	holdBeforeCommit(t, func() {
		arrived <- struct{}{}
		<-release
	})
	errs := make(chan error, 2)
	for _, m := range []struct{ id, parent string }{{"a", "b"}, {"b", "a"}} {
		go func() {
			parent := m.parent
			errs <- s.UpdateGroup(context.Background(), "shop", m.id, store.GroupUpdate{ParentID: &parent}, itAt, "cli:ops")
		}()
	}
	waitFor(t, arrived, "first move at the commit barrier")
	if loser := waitFor(t, errs, "conflicting move"); !errors.Is(loser, apperr.ErrUnavailable) {
		t.Fatalf("conflicting move = %v; want unavailable", loser)
	}
	close(release)
	if winner := waitFor(t, errs, "held move"); winner != nil {
		t.Fatalf("held move = %v; want nil", winner)
	}
	a, b := readGroupDoc(t, s, "shop", "a"), readGroupDoc(t, s, "shop", "b")
	underB := a.ParentID == "b" && slices.Equal(a.Ancestors, []string{"b"}) && b.ParentID == "" && len(b.Ancestors) == 0
	underA := b.ParentID == "a" && slices.Equal(b.Ancestors, []string{"a"}) && a.ParentID == "" && len(a.Ancestors) == 0
	if underB == underA {
		t.Fatalf("a = %+v, b = %+v; want exactly one move applied", a, b)
	}
	if tenant := readTenant(t, s, "shop"); tenant.GroupTreeVer != 3 || tenant.Ver != 1 {
		t.Fatalf("tenant = %+v; want group_tree_ver 3 and ver 1", tenant)
	}
}

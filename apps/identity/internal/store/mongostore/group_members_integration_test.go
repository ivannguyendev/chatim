package mongostore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func userGroupIDs(t *testing.T, s *Store, tenantID, id string) []string {
	t.Helper()
	doc := readUserDoc(t, s, tenantID, id)
	if doc.Groups == nil {
		t.Fatalf("user %s/%s groups = null; want an array", tenantID, id)
	}
	out := make([]string, 0, len(doc.Groups))
	for _, g := range doc.Groups {
		out = append(out, g.(string))
	}
	return out
}

func TestSetUserGroupsStoresASortedListAndBumpsTheVersionOnlyOnAChange(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "g-b", "")
	mustCreateGroup(t, s, "shop", "g-a", "")
	mustCreateUser(t, s, "shop", "lan", "Lan", "")
	setAt := itAt.Add(time.Minute)
	if err := s.SetUserGroups(t.Context(), "shop", "lan", []string{"g-b", "g-a"}, setAt, "cli:ops"); err != nil {
		t.Fatalf("SetUserGroups: %v", err)
	}
	user := readUserDoc(t, s, "shop", "lan")
	if got := userGroupIDs(t, s, "shop", "lan"); !slices.Equal(got, []string{"g-a", "g-b"}) || user.Ver != 2 ||
		!user.UpdatedAt.Equal(setAt) || user.UpdatedBy != "cli:ops" {
		t.Fatalf("user = %+v; want groups [g-a g-b], ver 2, updated at %v by cli:ops", user, setAt)
	}
	for _, id := range []string{"g-a", "g-b"} {
		if g := readGroupDoc(t, s, "shop", id); g.MembersVer != 1 || g.Ver != 1 {
			t.Fatalf("group %s = %+v; want members_ver 1 and ver 1", id, g)
		}
	}
	if err := s.SetUserGroups(t.Context(), "shop", "lan", []string{"g-a", "g-b"}, setAt.Add(time.Hour), "cli:other"); err != nil {
		t.Fatalf("SetUserGroups again: %v", err)
	}
	if again := readUserDoc(t, s, "shop", "lan"); again.Ver != 2 || !again.UpdatedAt.Equal(setAt) || again.UpdatedBy != "cli:ops" {
		t.Fatalf("user after the same list = %+v; want ver 2 updated at %v by cli:ops", again, setAt)
	}
	for i, ids := range [][]string{{}, nil} {
		if err := s.SetUserGroups(t.Context(), "shop", "lan", ids, setAt, "cli:ops"); err != nil {
			t.Fatalf("SetUserGroups empty: %v", err)
		}
		if got := userGroupIDs(t, s, "shop", "lan"); len(got) != 0 {
			t.Fatalf("groups after an empty list = %v; want []", got)
		}
		if again := readUserDoc(t, s, "shop", "lan"); again.Ver != int64(3) {
			t.Fatalf("user after empty list %d = %+v; want ver 3", i, again)
		}
	}
}

func TestSetUserGroupsRefusesAGroupListedTwice(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "g-a", "")
	mustCreateUser(t, s, "shop", "lan", "Lan", "")
	err := s.SetUserGroups(t.Context(), "shop", "lan", []string{"g-a", "g-a"}, itAt, "cli:ops")
	if !errors.Is(err, apperr.ErrInvalidArgument) || err.Error() != errGroupListedTwice.Error() {
		t.Fatalf("SetUserGroups with g-a twice = %v; want %v", err, errGroupListedTwice)
	}
	if user := readUserDoc(t, s, "shop", "lan"); user.Ver != 1 {
		t.Fatalf("user = %+v; want it unchanged", user)
	}
}

func TestSetUserGroupsWithAnUnknownDeletedOrForeignGroupIsNotFound(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateTenant(t, s, "other")
	mustCreateGroup(t, s, "shop", "g-a", "")
	mustCreateGroup(t, s, "other", "g-x", "")
	insertDeletedGroup(t, s, "shop", "gone")
	mustCreateUser(t, s, "shop", "lan", "Lan", "")
	if err := s.SetUserGroups(t.Context(), "shop", "lan", []string{"g-a"}, itAt, "cli:ops"); err != nil {
		t.Fatalf("SetUserGroups g-a: %v", err)
	}
	for _, ids := range [][]string{{"g-a", "ghost"}, {"gone"}, {"g-x"}} {
		if err := s.SetUserGroups(t.Context(), "shop", "lan", ids, itAt.Add(time.Hour), "cli:other"); !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("SetUserGroups %v = %v; want not found", ids, err)
		}
	}
	if got, user := userGroupIDs(t, s, "shop", "lan"), readUserDoc(t, s, "shop", "lan"); !slices.Equal(got, []string{"g-a"}) || user.Ver != 2 {
		t.Fatalf("user = %+v; want groups [g-a] and ver 2 kept", user)
	}
	if g := readGroupDoc(t, s, "shop", "g-a"); g.MembersVer != 1 {
		t.Fatalf("g-a members_ver = %d; want 1 after refused lists", g.MembersVer)
	}
}

func TestSetUserGroupsOfAnUnknownUserIsNotFound(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "g-a", "")
	mustCreateUser(t, s, "other", "lan", "Lan", "")
	for _, c := range []struct{ tenant, user string }{{"shop", "ghost"}, {"shop", "lan"}} {
		if err := s.SetUserGroups(t.Context(), c.tenant, c.user, []string{"g-a"}, itAt, "cli:ops"); !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("SetUserGroups %s/%s = %v; want not found", c.tenant, c.user, err)
		}
	}
	if g := readGroupDoc(t, s, "shop", "g-a"); g.MembersVer != 0 {
		t.Fatalf("g-a members_ver = %d; want 0", g.MembersVer)
	}
}

func TestSetUserGroupsRacingDeleteGroupGivesOneUnavailable(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "g", "")
	mustCreateUser(t, s, "shop", "binh", "Bình", "")
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	holdBeforeCommit(t, func() {
		arrived <- struct{}{}
		<-release
	})
	errs := make(chan error, 2)
	go func() {
		errs <- s.SetUserGroups(context.Background(), "shop", "binh", []string{"g"}, itAt, "cli:ops")
	}()
	go func() {
		_, err := s.DeleteGroup(context.Background(), "shop", "g", itAt, "cli:ops")
		errs <- err
	}()
	waitFor(t, arrived, "first command at the commit barrier")
	if loser := waitFor(t, errs, "conflicting command"); !errors.Is(loser, apperr.ErrUnavailable) {
		t.Fatalf("conflicting command = %v; want unavailable", loser)
	}
	close(release)
	if winner := waitFor(t, errs, "held command"); winner != nil {
		t.Fatalf("held command = %v; want nil", winner)
	}
	groups, g := userGroupIDs(t, s, "shop", "binh"), readGroupDoc(t, s, "shop", "g")
	assigned := slices.Equal(groups, []string{"g"}) && g.State == store.GroupLive && g.MembersVer == 1
	deleted := len(groups) == 0 && g.State == store.GroupDeleted && g.MembersVer == 1
	if assigned == deleted {
		t.Fatalf("user groups %v, group %+v; want exactly one command applied", groups, g)
	}
}

func TestSetUserGroupsAfterDeleteGroupIsNotFound(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "g", "")
	mustCreateUser(t, s, "shop", "binh", "Bình", "")
	if n, err := s.DeleteGroup(t.Context(), "shop", "g", itAt, "cli:ops"); err != nil || n != 0 {
		t.Fatalf("DeleteGroup = %d, %v; want 0, nil", n, err)
	}
	if err := s.SetUserGroups(t.Context(), "shop", "binh", []string{"g"}, itAt, "cli:ops"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("SetUserGroups to a deleted group = %v; want not found", err)
	}
}

func insertGroupMembers(t *testing.T, s *Store, tenantID, groupID string, n int) {
	t.Helper()
	docs := make([]any, 0, n)
	for i := range n {
		id := fmt.Sprintf("u%04d", i)
		docs = append(docs, bson.D{
			{Key: "_id", Value: tenantKey(tenantID, id)}, {Key: "tenant_id", Value: tenantID}, {Key: "id", Value: id},
			{Key: "display_name", Value: id}, {Key: "avatar_url", Value: ""}, {Key: "state", Value: store.StateActive},
			{Key: "groups", Value: bson.A{groupID}}, {Key: "ver", Value: int64(1)}, {Key: "created_at", Value: itAt},
			{Key: "updated_at", Value: itAt}, {Key: "updated_by", Value: jitBy},
		})
	}
	if _, err := s.db.Collection(usersCollection).InsertMany(t.Context(), docs); err != nil {
		t.Fatalf("insert %d members of %s: %v", n, groupID, err)
	}
}

func countUsersWhere(t *testing.T, s *Store, filter bson.D) int64 {
	t.Helper()
	n, err := s.db.Collection(usersCollection).CountDocuments(t.Context(), filter)
	if err != nil {
		t.Fatalf("count users %v: %v", filter, err)
	}
	return n
}

func countUsersIn(t *testing.T, s *Store, tenantID, groupID string) int64 {
	t.Helper()
	return countUsersWhere(t, s, bson.D{{Key: "tenant_id", Value: tenantID}, {Key: "groups", Value: groupID}})
}

func TestDeleteGroupWithALiveChildIsFailedPrecondition(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateChain(t, s, "shop", "a", "b")
	if _, err := s.DeleteGroup(t.Context(), "shop", "a", itAt, "cli:ops"); !errors.Is(err, apperr.ErrFailedPrecondition) ||
		err.Error() != errGroupHasChildren.Error() {
		t.Fatalf("DeleteGroup with a live child = %v; want %v", err, errGroupHasChildren)
	}
	if a := readGroupDoc(t, s, "shop", "a"); a.State != store.GroupLive || a.Ver != 1 {
		t.Fatalf("group a = %+v; want it live and unchanged", a)
	}
	for _, id := range []string{"b", "a"} {
		if n, err := s.DeleteGroup(t.Context(), "shop", id, itAt.Add(time.Minute), "cli:ops"); err != nil || n != 0 {
			t.Fatalf("DeleteGroup %s = %d, %v; want 0, nil", id, n, err)
		}
	}
	a := readGroupDoc(t, s, "shop", "a")
	if a.State != store.GroupDeleted || a.Ver != 2 || a.MembersVer != 1 || !a.UpdatedAt.Equal(itAt.Add(time.Minute)) || a.UpdatedBy != "cli:ops" {
		t.Fatalf("deleted group a = %+v; want state 2, ver 2, members_ver 1, updated by cli:ops", a)
	}
	if n, err := s.DeleteGroup(t.Context(), "shop", "a", itAt.Add(time.Hour), "cli:other"); err != nil || n != 0 {
		t.Fatalf("DeleteGroup again = %d, %v; want 0, nil", n, err)
	}
	if again := readGroupDoc(t, s, "shop", "a"); again.Ver != 2 || again.UpdatedBy != "cli:ops" {
		t.Fatalf("group a after a second delete = %+v; want it unchanged", again)
	}
	if tenant := readTenant(t, s, "shop"); tenant.GroupTreeVer != 4 || tenant.Ver != 1 {
		t.Fatalf("tenant = %+v; want group_tree_ver 4 (2 creates, 2 deletes) and ver 1", tenant)
	}
	if _, err := s.DeleteGroup(t.Context(), "shop", "ghost", itAt, "cli:ops"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("DeleteGroup of an unknown group = %v; want not found", err)
	}
}

func TestDeleteGroupRemovesTheGroupFromEveryMemberInBatches(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "g", "")
	insertGroupMembers(t, s, "shop", "g", 1200)
	insertGroupMembers(t, s, "other", "g", 1)
	var batches []int
	onBatch(t, func(n int) { batches = append(batches, n) })
	n, err := s.DeleteGroup(t.Context(), "shop", "g", itAt, "cli:ops")
	if err != nil || n != 1200 {
		t.Fatalf("DeleteGroup = %d, %v; want 1200, nil", n, err)
	}
	if !slices.Equal(batches, []int{500, 500, 200}) {
		t.Fatalf("batches = %v; want [500 500 200]", batches)
	}
	if left := countUsersIn(t, s, "shop", "g"); left != 0 {
		t.Fatalf("users still in g = %d; want 0", left)
	}
	if bumped := countUsersWhere(t, s, bson.D{{Key: "tenant_id", Value: "shop"}, {Key: "ver", Value: int64(2)}}); bumped != 1200 {
		t.Fatalf("users at ver 2 = %d; want 1200", bumped)
	}
	if u := readUserDoc(t, s, "shop", "u0007"); len(u.Groups) != 0 || u.Ver != 2 || !u.UpdatedAt.Equal(itAt) || u.UpdatedBy != "cli:ops" {
		t.Fatalf("user u0007 = %+v; want no groups, ver 2, updated by cli:ops", u)
	}
	if foreign := readUserDoc(t, s, "other", "u0000"); len(foreign.Groups) != 1 || foreign.Ver != 1 {
		t.Fatalf("user of another tenant = %+v; want it unchanged", foreign)
	}
}

func TestDeleteGroupInterruptedAfterTheFirstBatchContinuesOnReRun(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateGroup(t, s, "shop", "g", "")
	insertGroupMembers(t, s, "shop", "g", 1200)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var batches []int
	onBatch(t, func(n int) {
		batches = append(batches, n)
		cancel()
	})
	n, err := s.DeleteGroup(ctx, "shop", "g", itAt, "cli:ops")
	if !errors.Is(err, apperr.ErrUnavailable) || n != 500 {
		t.Fatalf("interrupted DeleteGroup = %d, %v; want 500, unavailable", n, err)
	}
	if g := readGroupDoc(t, s, "shop", "g"); g.State != store.GroupDeleted {
		t.Fatalf("group = %+v; want it deleted", g)
	}
	if left := countUsersIn(t, s, "shop", "g"); left != 700 {
		t.Fatalf("users still in g = %d; want 700", left)
	}
	onBatch(t, func(n int) { batches = append(batches, n) })
	n, err = s.DeleteGroup(t.Context(), "shop", "g", itAt, "cli:ops")
	if err != nil || n != 700 {
		t.Fatalf("re-run DeleteGroup = %d, %v; want 700, nil", n, err)
	}
	if !slices.Equal(batches, []int{500, 500, 200}) || countUsersIn(t, s, "shop", "g") != 0 {
		t.Fatalf("batches = %v; want [500 500 200] and no user left in g", batches)
	}
	if g := readGroupDoc(t, s, "shop", "g"); g.Ver != 2 || g.MembersVer != 1 {
		t.Fatalf("group after the re-run = %+v; want ver 2 and members_ver 1 from the first delete only", g)
	}
}

package recipients_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/recipients"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store/memstore"
)

const tenant = "t1"

const coreMemberPage = 1000

var (
	errDown      = errors.New("core down")
	errPageLimit = errors.New("invalid argument")
)

type fakeCore struct {
	rooms      map[uint64][]domain.Member
	membersOf  int
	listCalls  int
	failOn     string
	listLimits []int
}

func newCore() *fakeCore { return &fakeCore{rooms: map[uint64][]domain.Member{}} }

func (c *fakeCore) join(room uint64, users ...string) {
	for _, u := range users {
		c.rooms[room] = append(c.rooms[room], domain.Member{User: u, Role: "member", Active: true})
	}
	slices.SortFunc(c.rooms[room], func(a, b domain.Member) int { return strings.Compare(a.User, b.User) })
}

func (c *fakeCore) leave(room uint64, user string) {
	for i, m := range c.rooms[room] {
		if m.User == user {
			c.rooms[room][i].Active = false
		}
	}
}

func (c *fakeCore) MembersOf(_ context.Context, room uint64, users []string) ([]domain.Member, error) {
	c.membersOf++
	if c.failOn == "MembersOf" {
		return nil, errDown
	}
	var out []domain.Member
	for _, m := range c.rooms[room] {
		if slices.Contains(users, m.User) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (c *fakeCore) ListMembers(_ context.Context, room uint64, after domain.MemberCursor, limit int) ([]domain.Member, error) {
	c.listCalls++
	c.listLimits = append(c.listLimits, limit)
	switch {
	case c.failOn == "ListMembers":
		return nil, errDown
	case limit < 1 || limit > coreMemberPage:
		return nil, fmt.Errorf("%w: limit", errPageLimit)
	}
	var out []domain.Member
	for _, m := range c.rooms[room] {
		if m.User > after.User && len(out) < limit {
			out = append(out, m)
		}
	}
	return out, nil
}

type countingGroups struct {
	store.UserGroups
	of, byGroup int
}

func (g *countingGroups) Of(ctx context.Context, tenant string, users []string) (map[string][]string, error) {
	g.of++
	return g.UserGroups.Of(ctx, tenant, users)
}

func (g *countingGroups) MembersOfGroup(ctx context.Context, tenant, group, after string, limit int) ([]string, error) {
	g.byGroup++
	return g.UserGroups.MembersOfGroup(ctx, tenant, group, after, limit)
}

type fixture struct {
	core    *fakeCore
	groups  *countingGroups
	catalog *memstore.Groups
	r       recipients.Resolver
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{core: newCore(), groups: &countingGroups{UserGroups: memstore.NewUserGroups()}, catalog: memstore.NewGroups()}
	f.r = recipients.Resolver{Core: f.core, UserGroups: f.groups, Catalog: recipients.StoreCatalog{Groups: f.catalog}}
	return f
}

func (f *fixture) inGroups(t *testing.T, user string, groups ...string) {
	t.Helper()
	if _, err := f.groups.Set(t.Context(), []domain.UserGroups{{Tenant: tenant, User: user, Groups: groups, Ver: 1}}); err != nil {
		t.Fatalf("set groups of %s: %v", user, err)
	}
}

func (f *fixture) catalogue(t *testing.T, id string, active bool) {
	t.Helper()
	if _, err := f.catalog.Set(t.Context(), []domain.Group{{Tenant: tenant, ID: id, Name: id, Active: active, Ver: 1}}); err != nil {
		t.Fatalf("set group %s: %v", id, err)
	}
}

func users(prefix string, from, to int) []string {
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("%s%05d", prefix, i))
	}
	return out
}

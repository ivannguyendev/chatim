package roomgroups

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store/memstore"
)

type fakeMembers struct {
	ids []string
	err error
}

func (f fakeMembers) RoomMemberIDs(context.Context, uint64) ([]string, error) { return f.ids, f.err }

type chunkCounter struct {
	*memstore.UserGroups
	sizes []int
	err   error
}

func (c *chunkCounter) Of(ctx context.Context, tenant string, users []string) (map[string][]string, error) {
	c.sizes = append(c.sizes, len(users))
	if c.err != nil {
		return nil, c.err
	}
	return c.UserGroups.Of(ctx, tenant, users)
}

func TestBuildCollectsTheGroupsOfEveryMemberInChunks(t *testing.T) {
	ug := &chunkCounter{UserGroups: memstore.NewUserGroups()}
	users := make([]string, 2500)
	for i := range users {
		users[i] = fmt.Sprintf("u%04d", i)
	}
	items := []domain.UserGroups{
		{Tenant: "acme", User: "u0001", Groups: []string{"sale"}, Ver: 1},
		{Tenant: "acme", User: "u2400", Groups: []string{"dev", "sale"}, Ver: 1},
		{Tenant: "acme", User: "ghost", Groups: []string{"hr"}, Ver: 1},
		{Tenant: "globex", User: "u0002", Groups: []string{"ops"}, Ver: 1},
	}
	if _, err := ug.Set(t.Context(), items); err != nil {
		t.Fatalf("Set: %v", err)
	}
	b, err := NewBuilder(fakeMembers{ids: users}, ug)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	got, err := b.Build(t.Context(), "acme", 7)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ids := slices.Sorted(maps.Keys(got)); !slices.Equal(ids, []string{"dev", "sale"}) || !slices.Equal(ug.sizes, []int{1000, 1000, 500}) {
		t.Fatalf("Build = %v in chunks %v, want dev and sale in chunks 1000, 1000, 500", ids, ug.sizes)
	}
}

func TestBuildFailsWhenAReadFails(t *testing.T) {
	ug := &chunkCounter{UserGroups: memstore.NewUserGroups()}
	b, _ := NewBuilder(fakeMembers{err: errDown}, ug)
	if _, err := b.Build(t.Context(), "acme", 7); !errors.Is(err, errDown) {
		t.Fatalf("Build = %v, want the member read error", err)
	}
	ug.err = errDown
	b, _ = NewBuilder(fakeMembers{ids: []string{"a"}}, ug)
	if _, err := b.Build(t.Context(), "acme", 7); !errors.Is(err, errDown) {
		t.Fatalf("Build = %v, want the user groups error", err)
	}
	if _, err := NewBuilder(nil, ug); err == nil {
		t.Fatal("NewBuilder without members succeeded")
	}
}

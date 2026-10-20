package groups_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/groups"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store/memstore"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var errDown = errors.New("store down")

type countingGroups struct {
	*memstore.Groups
	mu    sync.Mutex
	loads int
	err   error
	gate  chan struct{}
}

func (g *countingGroups) All(ctx context.Context, tenant string) ([]domain.Group, error) {
	g.mu.Lock()
	g.loads++
	err, gate := g.err, g.gate
	g.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		return nil, err
	}
	return g.Groups.All(ctx, tenant)
}

func (g *countingGroups) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.loads
}

func seeded(t *testing.T) *countingGroups {
	t.Helper()
	g := &countingGroups{Groups: memstore.NewGroups()}
	set(t, g, domain.Group{Tenant: "acme", ID: "sale", Name: "Sales", Active: true, Ver: 1},
		domain.Group{Tenant: "acme", ID: "hr", Name: "HR", Active: false, Ver: 1},
		domain.Group{Tenant: "acme", ID: "dev", Name: "Developers", Active: true, Ver: 1},
		domain.Group{Tenant: "globex", ID: "ops", Name: "Ops", Active: true, Ver: 1})
	return g
}

func set(t *testing.T, g *countingGroups, items ...domain.Group) {
	t.Helper()
	if _, err := g.Set(t.Context(), items); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func idsOf(gs []domain.Group) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.ID
	}
	return out
}

func active(t *testing.T, c *groups.Catalog, tenant string) []string {
	t.Helper()
	gs, err := c.Active(t.Context(), tenant)
	if err != nil {
		t.Fatalf("Active(%s): %v", tenant, err)
	}
	return idsOf(gs)
}

func TestActiveListsTaggableGroupsByNameAndCachesPerTenant(t *testing.T) {
	g := seeded(t)
	c := groups.New(g)
	if got := active(t, c, "acme"); !slices.Equal(got, []string{"dev", "sale"}) {
		t.Fatalf("Active(acme) = %v, want dev then sale by name, hr left out", got)
	}
	active(t, c, "acme")
	if got := active(t, c, "globex"); !slices.Equal(got, []string{"ops"}) {
		t.Fatalf("Active(globex) = %v, want ops", got)
	}
	if n := g.count(); n != 2 {
		t.Fatalf("loads = %d, want one per tenant", n)
	}
	tag, err := c.Taggable(t.Context(), "acme")
	if err != nil || len(tag) != 2 || !tag["dev"] || !tag["sale"] || tag["hr"] {
		t.Fatalf("Taggable(acme) = %v, %v, want dev and sale", tag, err)
	}
}

func TestForgetAndTTLReloadTheCatalog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := seeded(t)
		c := groups.New(g)
		active(t, c, "acme")
		set(t, g, domain.Group{Tenant: "acme", ID: "hr", Name: "HR", Active: true, Ver: 2})
		if got := active(t, c, "acme"); !slices.Equal(got, []string{"dev", "sale"}) {
			t.Fatalf("cached Active = %v, want the old catalog before forget or ttl", got)
		}
		c.Forget("acme")
		if got := active(t, c, "acme"); !slices.Equal(got, []string{"dev", "hr", "sale"}) {
			t.Fatalf("Active after Forget = %v, want hr included", got)
		}
		set(t, g, domain.Group{Tenant: "acme", ID: "dev", Name: "Developers", Active: false, Ver: 2})
		time.Sleep(groups.TTL - time.Second)
		if got := active(t, c, "acme"); len(got) != 3 {
			t.Fatalf("Active before ttl = %v, want the cached three", got)
		}
		time.Sleep(time.Second)
		if got := active(t, c, "acme"); !slices.Equal(got, []string{"hr", "sale"}) {
			t.Fatalf("Active after ttl = %v, want dev gone", got)
		}
		if n := g.count(); n != 3 {
			t.Fatalf("loads = %d, want 3", n)
		}
	})
}

func TestAFailedLoadIsNotCached(t *testing.T) {
	g := seeded(t)
	g.err = errDown
	c := groups.New(g)
	if _, err := c.Active(t.Context(), "acme"); !errors.Is(err, errDown) {
		t.Fatalf("Active = %v, want the store error", err)
	}
	g.err = nil
	if got := active(t, c, "acme"); len(got) != 2 {
		t.Fatalf("Active after recovery = %v, want two groups", got)
	}
}

func TestALoadOverlappingAForgetIsNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := seeded(t)
		g.gate = make(chan struct{})
		c := groups.New(g)
		done := make(chan []string)
		go func() { done <- active(t, c, "acme") }()
		synctest.Wait()
		c.Forget("acme")
		close(g.gate)
		<-done
		g.gate = nil
		active(t, c, "acme")
		if n := g.count(); n != 2 {
			t.Fatalf("loads = %d, want the overlapped load dropped and a fresh one", n)
		}
	})
}

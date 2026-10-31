package directory_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

func TestApplyKeepsTheHigherVer(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntries([]store.DirectoryEntry{entry("shop", "lan", "Lan v3", 3)})
	c.Apply([]store.Change{userChange("shop", "lan", "Lan v2", 2), userChange("shop", "lan", "Lan v3 again", 3)})
	if got := displayName(c, "shop", "lan"); got != "Lan v3" {
		t.Fatalf("after older and equal vers name = %q; want Lan v3", got)
	}
	c.Apply([]store.Change{userChange("shop", "lan", "Lan v5", 5)})
	c.ApplyEntries([]store.DirectoryEntry{entry("shop", "lan", "Lan v4", 4)})
	if got := displayName(c, "shop", "lan"); got != "Lan v5" {
		t.Fatalf("after a newer change and an older load row name = %q; want Lan v5", got)
	}
	disabled := userChange("shop", "lan", "Lan v6", 6)
	disabled.User.State = store.StateDisabled
	disabled.User.AvatarURL = "https://cdn.example.com/lan.png"
	c.Apply([]store.Change{disabled})
	want := directory.User{ID: "lan", Entry: directory.Entry{DisplayName: "Lan v6", AvatarURL: "https://cdn.example.com/lan.png", State: store.StateDisabled, Ver: 6}}
	if got := c.Lookup("shop", []string{"lan"}); len(got) != 1 || got[0] != want {
		t.Fatalf("Lookup = %+v; want %+v", got, want)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d; want 1", c.Len())
	}
}

func TestUnknownStatesNeverReachTheCache(t *testing.T) {
	c := directory.NewCache()
	odd := userChange("shop", "lan", "Lan", 1)
	odd.User.State = 9
	c.Apply([]store.Change{odd})
	c.ApplyEntries([]store.DirectoryEntry{{TenantID: "shop", ID: "minh", DisplayName: "Minh", Ver: 1}})
	if c.Len() != 0 {
		t.Fatalf("Len = %d; want 0", c.Len())
	}
}

func TestGroupRecordsNeverTouchTheCache(t *testing.T) {
	c := directory.NewCache()
	c.Apply([]store.Change{userChange("shop", "sales", "Sales Person", 1)})
	c.Apply([]store.Change{
		groupChange("shop", "sales", "Sales Team", 9),
		groupChange("shop", "north", "North", 1),
		{Kind: store.ChangeUser, TenantID: "shop", ID: "gone", Ver: 4},
		{Kind: store.ChangeUser, TenantID: "shop", ID: "sales", Ver: 10},
	})
	if got := displayName(c, "shop", "sales"); got != "Sales Person" {
		t.Fatalf("user sales name = %q; want Sales Person", got)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d; want 1", c.Len())
	}
}

func TestLookupKeepsInputOrderAndSkipsMissing(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntries([]store.DirectoryEntry{entry("shop", "a", "A", 1), entry("shop", "b", "B", 1), entry("shop", "c", "C", 1), entry("other", "d", "D", 1)})
	got := c.Lookup("shop", []string{"c", "missing", "a", "d", "b", "a"})
	var ids []string
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	if want := []string{"c", "a", "b", "a"}; !slices.Equal(ids, want) {
		t.Fatalf("Lookup ids = %v; want %v", ids, want)
	}
	if got := c.Lookup("shop", nil); len(got) != 0 {
		t.Fatalf("Lookup(nil) = %v; want empty", got)
	}
}

func TestPositionAndReadinessStartEmpty(t *testing.T) {
	c := directory.NewCache()
	if c.Ready() || c.Position() != (store.Position{}) {
		t.Fatalf("new cache ready=%v position=%+v; want not ready at the zero position", c.Ready(), c.Position())
	}
	p := store.Position{Xid: 7, Kind: store.ChangeUser, Tenant: "shop", ID: "lan"}
	c.SetPosition(p)
	if c.Position() != p {
		t.Fatalf("Position = %+v; want %+v", c.Position(), p)
	}
}

package directory_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func TestApplyKeepsTheHigherVer(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntries([]store.DirectoryEntry{entry("shop", "lan", "Lan v3", 3)})
	c.ApplyEntry(entry("shop", "lan", "Lan v2", 2))
	c.ApplyEntry(entry("shop", "lan", "Lan v3 again", 3))
	if got := displayName(c, "shop", "lan"); got != "Lan v3" {
		t.Fatalf("after older and equal vers name = %q; want Lan v3", got)
	}
	c.ApplyEntry(entry("shop", "lan", "Lan v5", 5))
	c.ApplyEntries([]store.DirectoryEntry{entry("shop", "lan", "Lan v4", 4)})
	if got := displayName(c, "shop", "lan"); got != "Lan v5" {
		t.Fatalf("after a newer change and an older load row name = %q; want Lan v5", got)
	}
	disabled := entry("shop", "lan", "Lan v6", 6)
	disabled.State = store.StateDisabled
	disabled.AvatarURL = "https://cdn.example.com/lan.png"
	c.ApplyEntry(disabled)
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
	odd := entry("shop", "lan", "Lan", 1)
	odd.State = 9
	c.ApplyEntry(odd)
	c.ApplyEntries([]store.DirectoryEntry{{TenantID: "shop", ID: "minh", DisplayName: "Minh", Ver: 1}})
	if c.Len() != 0 {
		t.Fatalf("Len = %d; want 0", c.Len())
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

func TestLookupOneFindsOnlyTheTenantUser(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntries([]store.DirectoryEntry{entry("shop", "lan", "Lan", 3), entry("other", "minh", "Minh", 1)})
	want := directory.User{ID: "lan", Entry: directory.Entry{DisplayName: "Lan", State: store.StateActive, Ver: 3}}
	if got, ok := c.LookupOne("shop", "lan"); !ok || got != want {
		t.Fatalf("LookupOne(shop, lan) = %+v, %v; want %+v, true", got, ok, want)
	}
	for _, tc := range []struct{ tenant, id string }{{"shop", "minh"}, {"shop", "missing"}, {"other", "lan"}} {
		if got, ok := c.LookupOne(tc.tenant, tc.id); ok || got != (directory.User{}) {
			t.Fatalf("LookupOne(%s, %s) = %+v, %v; want zero, false", tc.tenant, tc.id, got, ok)
		}
	}
}

func TestReadinessAndTokenStartEmpty(t *testing.T) {
	c := directory.NewCache()
	if c.Ready() || len(c.Token()) != 0 || c.Len() != 0 {
		t.Fatalf("new cache ready=%v token=%q len=%d; want not ready, no token, empty", c.Ready(), c.Token(), c.Len())
	}
}

func TestReplaceSwapsTheMapAndTheTokenAndMarksReady(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntry(entry("shop", "old", "Old", 1))
	if c.Ready() || len(c.Token()) != 0 {
		t.Fatalf("ready=%v token=%q before Replace", c.Ready(), c.Token())
	}
	c.Replace(map[store.UserKey]directory.Entry{{TenantID: "shop", ID: "lan"}: {DisplayName: "Lan", State: store.StateActive, Ver: 2}}, store.StreamToken("T0"))
	if !c.Ready() || string(c.Token()) != "T0" || c.Len() != 1 || displayName(c, "shop", "old") != "" || displayName(c, "shop", "lan") != "Lan" {
		t.Fatalf("after Replace ready=%v token=%q len=%d", c.Ready(), c.Token(), c.Len())
	}
	c.ApplyEntry(entry("shop", "lan", "Lan v1", 1))
	c.ApplyEntry(entry("shop", "lan", "Lan v3", 3))
	c.SetToken(store.StreamToken("T1"))
	if displayName(c, "shop", "lan") != "Lan v3" || string(c.Token()) != "T1" {
		t.Fatalf("lan=%q token=%q; want Lan v3 and T1", displayName(c, "shop", "lan"), c.Token())
	}
	c.SetToken(nil)
	if !c.Ready() || len(c.Token()) != 0 {
		t.Fatalf("ready=%v token=%q after clearing the token; want still ready without a token", c.Ready(), c.Token())
	}
}

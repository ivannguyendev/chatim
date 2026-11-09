package directory_test

import (
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func subjectEntry(tenant, id, name, realm, sub string) store.DirectoryEntry {
	e := entry(tenant, id, name, 1)
	e.Realm, e.Sub = realm, sub
	return e
}

func subjectID(t *testing.T, c *directory.Cache, tenant, realm, sub string) string {
	t.Helper()
	u, ok := c.LookupSubject(tenant, realm, sub)
	if !ok {
		return ""
	}
	return u.ID
}

func TestLookupSubjectFindsAUserFromTheLoadedPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, subjectEntry("shop", "u7f3a", "Lan", "staff", "lan@shop"), entry("shop", "plain", "Plain", 1))
		u, ok := r.cache.LookupSubject("shop", "staff", "lan@shop")
		if !ok || u.ID != "u7f3a" || u.DisplayName != "Lan" || u.State != store.StateActive {
			t.Fatalf("LookupSubject = %+v, %v; want u7f3a Lan active", u, ok)
		}
	})
}

func TestLookupSubjectFindsAUserThatArrivedOnTheStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := startedMirror(t, entry("shop", "lan", "Lan", 1))
		r.src.stream(t, 0).send(userEvent(subjectEntry("shop", "u9c21", "Mai", "staff", "mai@shop"), "T1"))
		synctest.Wait()
		if got := subjectID(t, r.cache, "shop", "staff", "mai@shop"); got != "u9c21" {
			t.Fatalf("LookupSubject after the stream event = %q; want u9c21", got)
		}
	})
}

func TestApplyEntryLeavesAUserWithoutRealmOrSubOutOfTheSubjects(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntry(entry("shop", "lan", "Lan", 1))
	c.ApplyEntry(subjectEntry("shop", "mai", "Mai", "staff", ""))
	c.ApplyEntry(subjectEntry("shop", "hoa", "Hoa", "", "hoa@shop"))
	for _, k := range []struct{ realm, sub string }{{"", ""}, {"staff", ""}, {"", "hoa@shop"}} {
		if got := subjectID(t, c, "shop", k.realm, k.sub); got != "" {
			t.Fatalf("LookupSubject(%q, %q) = %q; want not found", k.realm, k.sub, got)
		}
	}
	if c.Len() != 3 {
		t.Fatalf("users = %d; want 3 in the user map", c.Len())
	}
}

func TestLookupSubjectNeverCrossesTenants(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntry(subjectEntry("shop", "u7f3a", "Lan", "staff", "lan@corp"))
	if got := subjectID(t, c, "mall", "staff", "lan@corp"); got != "" {
		t.Fatalf("mall lookup = %q; want nothing from shop", got)
	}
	c.ApplyEntry(subjectEntry("mall", "u0b88", "Lan at mall", "staff", "lan@corp"))
	if shop, mall := subjectID(t, c, "shop", "staff", "lan@corp"), subjectID(t, c, "mall", "staff", "lan@corp"); shop != "u7f3a" || mall != "u0b88" {
		t.Fatalf("shop=%q mall=%q; want each tenant its own user", shop, mall)
	}
	if got := subjectID(t, c, "shop", "guests", "lan@corp"); got != "" {
		t.Fatalf("other realm lookup = %q; want not found", got)
	}
}

func TestLookupSubjectReturnsADisabledUserWithItsState(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntry(subjectEntry("shop", "u7f3a", "Lan", "staff", "lan@shop"))
	disabled := subjectEntry("shop", "u7f3a", "Lan", "staff", "lan@shop")
	disabled.State, disabled.Ver = store.StateDisabled, 2
	c.ApplyEntries([]store.DirectoryEntry{disabled})
	u, ok := c.LookupSubject("shop", "staff", "lan@shop")
	if !ok || u.ID != "u7f3a" || u.State != store.StateDisabled {
		t.Fatalf("LookupSubject = %+v, %v; want u7f3a disabled", u, ok)
	}
}

func TestReplaceSwapsTheSubjects(t *testing.T) {
	c := directory.NewCache()
	c.ApplyEntry(subjectEntry("shop", "old", "Old", "staff", "old@shop"))
	users := map[store.UserKey]directory.Entry{{TenantID: "shop", ID: "u7f3a"}: {DisplayName: "Lan", State: store.StateActive, Ver: 1}}
	subjects := map[store.SubjectKey]string{{TenantID: "shop", Realm: "staff", Sub: "lan@shop"}: "u7f3a"}
	c.Replace(users, subjects, store.StreamToken("T0"))
	if old, lan := subjectID(t, c, "shop", "staff", "old@shop"), subjectID(t, c, "shop", "staff", "lan@shop"); old != "" || lan != "u7f3a" {
		t.Fatalf("old=%q lan=%q after Replace; want old gone and lan found", old, lan)
	}
}

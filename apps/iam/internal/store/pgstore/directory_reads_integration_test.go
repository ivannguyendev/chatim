package pgstore_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	binhID = "a4aaaaaaaaaaaaaaaaaaaaaaaa"
	settle = 10 * time.Second
)

func readPage(t *testing.T, s fixture, after store.Position, limit int) store.ChangePage {
	t.Helper()
	page, err := s.Changes(context.Background(), after, limit)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	return page
}

func drain(t *testing.T, s fixture, after store.Position, limit int) ([]store.Change, store.Position) {
	t.Helper()
	var all []store.Change
	deadline := time.Now().Add(settle)
	for {
		page := readPage(t, s, after, limit)
		all = append(all, page.Changes...)
		after = page.Next
		if len(page.Changes) > 0 {
			continue
		}
		if !page.PendingAboveHorizon {
			return all, after
		}
		if time.Now().After(deadline) {
			t.Fatalf("changes still above the horizon after %v", settle)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func ids(changes []store.Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.ID)
	}
	return out
}

func rename(t *testing.T, conn execer, id, name string) {
	t.Helper()
	exec(t, conn, "UPDATE accounts SET display_name = $2 WHERE tenant_id = 'shop' AND id = $1", id, name)
}

func seedShop(t *testing.T, s fixture, accounts ...string) {
	t.Helper()
	seedTenant(t, s, "shop")
	for i, id := range accounts {
		seedAccount(t, s, "shop", id, "user"+string(rune('a'+i)))
	}
}

func TestOutOfOrderCommitsAreNeverSkipped(t *testing.T) {
	s := openStore(t)
	seedShop(t, s, lanID, minhID)
	_, start := drain(t, s, store.Position{}, 100)

	first := connect(t, s.uri)
	exec(t, first, "BEGIN")
	exec(t, first, "SELECT pg_current_xact_id()")
	rename(t, first, lanID, "Lan 2")
	rename(t, s.raw, minhID, "Minh 2")

	page := readPage(t, s, start, 100)
	if slices.Contains(ids(page.Changes), minhID) {
		t.Fatalf("Changes returned %v while the older transaction is open", ids(page.Changes))
	}
	if page.Next != start {
		t.Fatalf("Next moved to %+v while the older transaction is open; want %+v", page.Next, start)
	}

	exec(t, first, "COMMIT")
	got, _ := drain(t, s, start, 100)
	if want := []string{lanID, minhID}; !slices.Equal(ids(got), want) {
		t.Fatalf("after commit Changes = %v; want %v", ids(got), want)
	}
	if got[0].User.DisplayName != "Lan 2" || got[1].User.DisplayName != "Minh 2" {
		t.Fatalf("names = %q, %q", got[0].User.DisplayName, got[1].User.DisplayName)
	}
}

func TestReadingFromTheStartReturnsTheWholeDirectory(t *testing.T) {
	s := openStore(t)
	seedShop(t, s, lanID, minhID)
	sales := group("sales", "")
	north := group("north", "sales")
	quiet := group("quiet", "")
	quiet.Mentionable = false
	createGroups(t, s, sales, north, quiet)
	ctx := context.Background()
	if err := s.SetAccountGroups(ctx, "shop", lanID, []string{"sales", "north"}, audit("account_groups_set")); err != nil {
		t.Fatalf("SetAccountGroups: %v", err)
	}
	avatar := "https://cdn.example/lan.png"
	if err := s.SetProfile(ctx, "shop", lanID, store.ProfileUpdate{AvatarURL: &avatar}, audit("profile_set")); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}

	got, _ := drain(t, s, store.Position{}, 100)
	users, groups := map[string]store.Change{}, map[string]store.Change{}
	for _, c := range got {
		switch c.Kind {
		case store.ChangeUser:
			if c.User == nil || c.Group != nil {
				t.Fatalf("user change %s = %+v", c.ID, c)
			}
			users[c.ID] = c
		case store.ChangeGroup:
			if c.Group == nil || c.User != nil {
				t.Fatalf("group change %s = %+v", c.ID, c)
			}
			groups[c.ID] = c
		}
	}
	if len(users) != 2 || len(groups) != 3 || len(got) != 5 {
		t.Fatalf("got %d records, users %v, groups %v", len(got), users, groups)
	}
	lan := users[lanID]
	wantLan := store.UserChange{State: store.StateActive, DisplayName: "usera", AvatarURL: avatar, Groups: []string{"north", "sales"}}
	if lan.TenantID != "shop" || !reflect.DeepEqual(*lan.User, wantLan) || lan.Ver != changeVer(t, s, userKind, "shop", lanID) {
		t.Fatalf("lan = %+v %+v; want %+v", lan, *lan.User, wantLan)
	}
	if g := users[minhID].User.Groups; g == nil || len(g) != 0 {
		t.Fatalf("minh groups = %#v; want empty", g)
	}
	wantNorth := store.GroupChange{Name: "Group north", ParentID: "sales", Mentionable: true, State: store.GroupLive}
	if n := groups["north"]; !reflect.DeepEqual(*n.Group, wantNorth) {
		t.Fatalf("north = %+v; want %+v", *n.Group, wantNorth)
	}
	if q := groups["quiet"].Group; q.Mentionable || q.ParentID != "" {
		t.Fatalf("quiet = %+v", *q)
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[store.Change](), reflect.TypeFor[store.UserChange](), reflect.TypeFor[store.DirectoryEntry]()} {
		for f := range typ.Fields() {
			if name := strings.ToLower(f.Name); strings.Contains(name, "email") || strings.Contains(name, "username") {
				t.Fatalf("%s has field %s", typ, f.Name)
			}
		}
	}
}

func TestPagesResumeInsideOneTransactionsRows(t *testing.T) {
	s := openStore(t)
	seedShop(t, s, lanID, minhID, anID, binhID)
	_, start := drain(t, s, store.Position{}, 100)
	exec(t, s.raw, "UPDATE accounts SET display_name = display_name || '!' WHERE tenant_id = 'shop'")
	createGroups(t, s, group(lanID, ""))

	changes, _ := drain(t, s, start, 1)
	var got []string
	for _, c := range changes {
		got = append(got, kindID(c))
	}
	want := []string{"1/" + lanID, "1/" + minhID, "1/" + anID, "1/" + binhID, "2/" + lanID}
	if !slices.Equal(got, want) {
		t.Fatalf("walk = %v; want %v", got, want)
	}
}

func kindID(c store.Change) string {
	return string(rune('0'+c.Kind)) + "/" + c.ID
}

func TestPendingAboveHorizonIsTrueWhileAnOlderTransactionIsOpen(t *testing.T) {
	s := openStore(t)
	seedShop(t, s, lanID, minhID)
	_, start := drain(t, s, store.Position{}, 100)

	first := connect(t, s.uri)
	exec(t, first, "BEGIN")
	exec(t, first, "SELECT pg_current_xact_id()")
	rename(t, s.raw, minhID, "Minh 2")
	if page := readPage(t, s, start, 100); !page.PendingAboveHorizon || len(page.Changes) != 0 {
		t.Fatalf("while open: pending = %v, changes = %v; want true, none", page.PendingAboveHorizon, ids(page.Changes))
	}
	exec(t, first, "COMMIT")
	got, end := drain(t, s, start, 100)
	if !slices.Equal(ids(got), []string{minhID}) {
		t.Fatalf("after commit = %v", ids(got))
	}
	if page := readPage(t, s, end, 100); page.PendingAboveHorizon {
		t.Fatalf("pending after catching up")
	}
}

func TestLoadStartThenChangesCatchesWritesMadeDuringTheLoad(t *testing.T) {
	s := openStore(t)
	seedShop(t, s, lanID, minhID)
	ctx := context.Background()
	start, err := s.LoadStart(ctx)
	if err != nil {
		t.Fatalf("LoadStart: %v", err)
	}
	if start.Xid == 0 || start.Kind != 0 || start.Tenant != "" || start.ID != "" {
		t.Fatalf("LoadStart = %+v", start)
	}
	name := "Minh 2"
	if err := s.SetProfile(ctx, "shop", minhID, store.ProfileUpdate{DisplayName: &name}, audit("profile_set")); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	got, _ := drain(t, s, start, 100)
	i := slices.IndexFunc(got, func(c store.Change) bool { return c.ID == minhID })
	if i < 0 || got[i].User.DisplayName != name {
		t.Fatalf("Changes after LoadStart = %v; want %s renamed", ids(got), minhID)
	}
}

func TestLoadPageWalksAllAccountsInKeyOrder(t *testing.T) {
	s := openStore(t)
	seedShop(t, s, minhID, lanID, binhID)
	seedTenant(t, s, "acme")
	seedAccount(t, s, "acme", anID, "an")
	ctx := context.Background()
	var got []store.DirectoryEntry
	var after store.AccountKey
	for {
		page, err := s.LoadPage(ctx, after, 2)
		if err != nil {
			t.Fatalf("LoadPage: %v", err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		last := page[len(page)-1]
		after = store.AccountKey{TenantID: last.TenantID, ID: last.ID}
	}
	var keys []string
	for _, e := range got {
		keys = append(keys, e.TenantID+"/"+e.ID)
		if e.State != store.StateActive || e.DisplayName == "" || e.Ver != changeVer(t, s, userKind, e.TenantID, e.ID) {
			t.Fatalf("entry %+v", e)
		}
	}
	want := []string{"acme/" + anID, "shop/" + lanID, "shop/" + minhID, "shop/" + binhID}
	if !slices.Equal(keys, want) {
		t.Fatalf("LoadPage walk = %v; want %v", keys, want)
	}
}

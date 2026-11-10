package admincmd_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const (
	lanID  = "a1aaaaaaaaaaaaaaaaaaaaaaaa"
	minhID = "a2aaaaaaaaaaaaaaaaaaaaaaaa"
)

func seedUser(t *testing.T, h *harness, tenant, id, name string) {
	t.Helper()
	u := store.NewUser{TenantID: tenant, Realm: "test", Sub: id, CandidateID: id, DisplayName: name, At: time.Now(), By: "test"}
	linked, err := h.store().LinkUser(context.Background(), u)
	if err != nil || linked != (store.LinkedUser{ID: id, Result: store.LinkCreated}) {
		t.Fatalf("seed user %s = %+v, %v; want it created", id, linked, err)
	}
}

func newShop(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.run("migrate", "-apply")
	h.run("tenant-create", "-id", "shop", "-name", "Shop")
	return h
}

func (h *harness) fail(want int, args ...string) string {
	h.t.Helper()
	h.logs.Reset()
	if code := admincmd.Run(context.Background(), h.env, args); code != want {
		h.t.Fatalf("Run(%v) = %d; want %d; logs: %s", args, code, want, h.logs.String())
	}
	return h.logs.String()
}

func readGroup(t *testing.T, h *harness, id string) store.Group {
	t.Helper()
	g, err := h.store().Group(context.Background(), "shop", id)
	if err != nil {
		t.Fatalf("Group %s: %v", id, err)
	}
	return g
}

func wantGroup(t *testing.T, h *harness, id, name, parent string, mentionable bool, step string) {
	t.Helper()
	g := readGroup(t, h, id)
	if g.Name != name || g.ParentID != parent || g.Mentionable != mentionable || g.State != store.GroupLive || g.UpdatedBy != "admin-cli" {
		t.Fatalf("%s: group %s = %+v; want name %q parent %q mentionable %v", step, id, g, name, parent, mentionable)
	}
}

func TestGroupCommands(t *testing.T) {
	h := newShop(t)
	if out := h.run("group-create", "-tenant", "shop", "-id", "sales", "-name", "  Sales  "); out != "group sales created\n" {
		t.Fatalf("group-create output = %q", out)
	}
	h.run("group-create", "-tenant", "shop", "-id", "sales-hn", "-name", "Sales HN", "-parent", "sales", "-mentionable=false")
	wantGroup(t, h, "sales", "Sales", "", true, "create")
	wantGroup(t, h, "sales-hn", "Sales HN", "sales", false, "create child")
	h.run("group-update", "-tenant", "shop", "-id", "sales-hn", "-name", "Hà Nội")
	wantGroup(t, h, "sales-hn", "Hà Nội", "sales", false, "rename")
	h.run("group-update", "-tenant", "shop", "-id", "sales-hn", "-mentionable=true")
	wantGroup(t, h, "sales-hn", "Hà Nội", "sales", true, "mentionable")
	if logs := h.fail(1, "group-update", "-tenant", "shop", "-id", "sales", "-parent", "sales-hn"); !strings.Contains(logs, "group cycle") {
		t.Fatalf("cycle logs = %s; want the cycle message", logs)
	}
	wantGroup(t, h, "sales", "Sales", "", true, "after the refused cycle")
	h.run("group-update", "-tenant", "shop", "-id", "sales-hn", "-parent", "")
	wantGroup(t, h, "sales-hn", "Hà Nội", "", true, "move to root")
	h.fail(1, "group-create", "-tenant", "shop", "-id", "bad", "-name", "Bad", "-parent", "missing")
	h.fail(1, "group-create", "-tenant", "ghost", "-id", "sales", "-name", "Sales")
	h.fail(1, "group-create", "-tenant", "shop", "-id", "bad id", "-name", "Bad")
	h.fail(1, "group-update", "-tenant", "shop", "-id", "sales", "-name", " ")
	h.fail(2, "group-update", "-tenant", "shop", "-id", "sales")
	seedUser(t, h, "shop", lanID, "lan")
	h.run("user-groups", "-tenant", "shop", "-user", lanID, "-groups", "sales-hn,sales")
	if out := h.run("group-delete", "-tenant", "shop", "-id", "sales-hn"); out != "group sales-hn deleted, 1 users updated\n" {
		t.Fatalf("group-delete output = %q", out)
	}
	if g := readGroup(t, h, "sales-hn"); g.State != store.GroupDeleted {
		t.Fatalf("state after delete = %d; want deleted", g.State)
	}
	if got := userGroupsOf(t, h, lanID); !slices.Equal(got, []string{"sales"}) {
		t.Fatalf("groups after delete = %v; want only sales", got)
	}
	if out := h.run("group-delete", "-tenant", "shop", "-id", "sales-hn"); out != "group sales-hn deleted, 0 users updated\n" {
		t.Fatalf("second group-delete output = %q", out)
	}
	h.fail(1, "group-delete", "-tenant", "shop", "-id", "never")
}

func userGroupsOf(t *testing.T, h *harness, id string) []string {
	t.Helper()
	var doc struct {
		Groups []string `bson:"groups"`
	}
	h.find("users", "shop"+keySeparator+id, &doc)
	slices.Sort(doc.Groups)
	return doc.Groups
}

func profileOf(t *testing.T, h *harness, id string) (name, avatar string) {
	t.Helper()
	var doc struct {
		DisplayName string `bson:"display_name"`
		AvatarURL   string `bson:"avatar_url"`
	}
	h.find("users", "shop"+keySeparator+id, &doc)
	return doc.DisplayName, doc.AvatarURL
}

func TestUserGroupsAndUserSetCommands(t *testing.T) {
	h := newShop(t)
	seedUser(t, h, "shop", lanID, "lan")
	h.run("group-create", "-tenant", "shop", "-id", "sales", "-name", "Sales")
	h.run("group-create", "-tenant", "shop", "-id", "north", "-name", "North")
	if out := h.run("user-groups", "-tenant", "shop", "-user", lanID, "-groups", " sales , north "); out != "user "+lanID+" groups: sales, north\n" {
		t.Fatalf("user-groups output = %q", out)
	}
	if got := userGroupsOf(t, h, lanID); !slices.Equal(got, []string{"north", "sales"}) {
		t.Fatalf("groups = %v", got)
	}
	h.fail(1, "user-groups", "-tenant", "shop", "-user", lanID, "-groups", "sales,missing")
	h.fail(1, "user-groups", "-tenant", "shop", "-user", lanID, "-groups", "sales,sales")
	h.fail(1, "user-groups", "-tenant", "shop", "-user", lanID, "-groups", "sales,")
	h.fail(1, "user-groups", "-tenant", "shop", "-user", minhID, "-groups", "sales")
	if got := userGroupsOf(t, h, lanID); !slices.Equal(got, []string{"north", "sales"}) {
		t.Fatalf("groups after refused changes = %v", got)
	}
	if out := h.run("user-groups", "-tenant", "shop", "-user", lanID, "-groups", ""); out != "user "+lanID+" groups: none\n" {
		t.Fatalf("user-groups clear output = %q", out)
	}
	if got := userGroupsOf(t, h, lanID); len(got) != 0 {
		t.Fatalf("groups after clear = %v", got)
	}
	if out := h.run("user-set", "-tenant", "shop", "-user", lanID, "-name", "Lan N.", "-avatar", "https://cdn.example/lan.png"); out != "user "+lanID+" updated\n" {
		t.Fatalf("user-set output = %q", out)
	}
	h.fail(1, "user-set", "-tenant", "shop", "-user", lanID, "-avatar", "http://cdn.example/lan.png")
	h.fail(1, "user-set", "-tenant", "shop", "-user", lanID, "-name", "Bad", "-avatar", "http://cdn.example/lan.png")
	if name, avatar := profileOf(t, h, lanID); name != "Lan N." || avatar != "https://cdn.example/lan.png" {
		t.Fatalf("profile after refused changes = %q %q", name, avatar)
	}
	h.run("user-set", "-tenant", "shop", "-user", lanID, "-avatar", "")
	if name, avatar := profileOf(t, h, lanID); name != "Lan N." || avatar != "" {
		t.Fatalf("profile after clearing the avatar = %q %q", name, avatar)
	}
	h.fail(1, "user-set", "-tenant", "shop", "-user", minhID, "-name", "Minh")
	h.fail(2, "user-set", "-tenant", "shop", "-user", lanID)
	h.fail(2, "user-groups", "-tenant", "shop", "-user", lanID)
	var doc struct {
		UpdatedBy string `bson:"updated_by"`
	}
	if h.find("users", "shop"+keySeparator+lanID, &doc); doc.UpdatedBy != "admin-cli" {
		t.Fatalf("updated_by = %q; want admin-cli", doc.UpdatedBy)
	}
}

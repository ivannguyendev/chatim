package admincmd_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

func newShop(t *testing.T) (*harness, *pgx.Conn) {
	t.Helper()
	h := newHarness(t)
	h.run("migrate", "-apply")
	h.run("tenant-create", "-id", "shop", "-name", "Shop")
	raw, err := pgx.Connect(context.Background(), h.uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close(context.Background()) })
	return h, raw
}

func (h *harness) fail(want int, args ...string) string {
	h.t.Helper()
	h.logs.Reset()
	if code := admincmd.Run(context.Background(), h.env, args); code != want {
		h.t.Fatalf("Run(%v) = %d; want %d; logs: %s", args, code, want, h.logs.String())
	}
	return h.logs.String()
}

func wantCLIAudits(t *testing.T, raw *pgx.Conn, action string, want int) {
	t.Helper()
	var got, others int
	err := raw.QueryRow(context.Background(),
		"SELECT count(*) FILTER (WHERE actor = 'admin-cli'), count(*) FILTER (WHERE actor <> 'admin-cli') FROM audit_events WHERE tenant_id = 'shop' AND action = $1",
		action).Scan(&got, &others)
	if err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	if got != want || others != 0 {
		t.Fatalf("audit %s = %d by admin-cli, %d by others; want %d and 0", action, got, others, want)
	}
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
	h, raw := newShop(t)
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
	if out := h.run("group-delete", "-tenant", "shop", "-id", "sales-hn"); out != "group sales-hn deleted\n" {
		t.Fatalf("group-delete output = %q", out)
	}
	if g := readGroup(t, h, "sales-hn"); g.State != store.GroupDeleted {
		t.Fatalf("state after delete = %d; want deleted", g.State)
	}
	h.fail(1, "group-delete", "-tenant", "shop", "-id", "sales-hn")
	wantCLIAudits(t, raw, "group_create", 2)
	wantCLIAudits(t, raw, "group_update", 3)
	wantCLIAudits(t, raw, "group_delete", 1)
}

func accountGroupsOf(t *testing.T, raw *pgx.Conn, username string) []string {
	t.Helper()
	rows, err := raw.Query(context.Background(),
		"SELECT g.group_id FROM account_groups g JOIN accounts a ON a.tenant_id = g.tenant_id AND a.id = g.account_id WHERE a.tenant_id = 'shop' AND a.username = $1 ORDER BY g.group_id",
		username)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func profileOf(t *testing.T, raw *pgx.Conn, username string) (name, avatar string) {
	t.Helper()
	err := raw.QueryRow(context.Background(), "SELECT display_name, avatar_url FROM accounts WHERE tenant_id = 'shop' AND username = $1", username).Scan(&name, &avatar)
	if err != nil {
		t.Fatal(err)
	}
	return name, avatar
}

func TestAccountGroupAndProfileCommands(t *testing.T) {
	h, raw := newShop(t)
	h.run("account-create", "-tenant", "shop", "-username", "Lan", "-name", "  Lan Nguyễn ")
	h.run("account-create", "-tenant", "shop", "-username", "minh")
	if name, _ := profileOf(t, raw, "lan"); name != "Lan Nguyễn" {
		t.Fatalf("display name = %q; want the normalised -name", name)
	}
	if name, _ := profileOf(t, raw, "minh"); name != "minh" {
		t.Fatalf("display name = %q; want the username", name)
	}
	h.fail(1, "account-create", "-tenant", "shop", "-username", "hoa", "-name", "")
	h.fail(1, "account-create", "-tenant", "ghost", "-username", "hoa")
	h.run("group-create", "-tenant", "shop", "-id", "sales", "-name", "Sales")
	h.run("group-create", "-tenant", "shop", "-id", "north", "-name", "North")
	if out := h.run("account-groups", "-tenant", "shop", "-username", "lan", "-groups", " sales , north "); out != "account lan groups: sales, north\n" {
		t.Fatalf("account-groups output = %q", out)
	}
	if got := accountGroupsOf(t, raw, "lan"); !slices.Equal(got, []string{"north", "sales"}) {
		t.Fatalf("groups = %v", got)
	}
	h.fail(1, "account-groups", "-tenant", "shop", "-username", "lan", "-groups", "sales,missing")
	h.fail(1, "account-groups", "-tenant", "shop", "-username", "lan", "-groups", "sales,sales")
	h.fail(1, "account-groups", "-tenant", "shop", "-username", "lan", "-groups", "sales,")
	h.fail(1, "account-groups", "-tenant", "shop", "-username", "nobody", "-groups", "sales")
	if got := accountGroupsOf(t, raw, "lan"); !slices.Equal(got, []string{"north", "sales"}) {
		t.Fatalf("groups after refused changes = %v", got)
	}
	if out := h.run("account-groups", "-tenant", "shop", "-username", "lan", "-groups", ""); out != "account lan groups: none\n" {
		t.Fatalf("account-groups clear output = %q", out)
	}
	if got := accountGroupsOf(t, raw, "lan"); len(got) != 0 {
		t.Fatalf("groups after clear = %v", got)
	}
	if out := h.run("profile-set", "-tenant", "shop", "-username", "lan", "-name", "Lan N.", "-avatar", "https://cdn.example/lan.png"); out != "profile updated for lan\n" {
		t.Fatalf("profile-set output = %q", out)
	}
	h.fail(1, "profile-set", "-tenant", "shop", "-username", "lan", "-avatar", "http://cdn.example/lan.png")
	h.fail(1, "profile-set", "-tenant", "shop", "-username", "lan", "-name", "Bad", "-avatar", "http://cdn.example/lan.png")
	if name, avatar := profileOf(t, raw, "lan"); name != "Lan N." || avatar != "https://cdn.example/lan.png" {
		t.Fatalf("profile after refused changes = %q %q", name, avatar)
	}
	h.run("profile-set", "-tenant", "shop", "-username", "lan", "-avatar", "")
	if name, avatar := profileOf(t, raw, "lan"); name != "Lan N." || avatar != "" {
		t.Fatalf("profile after clearing the avatar = %q %q", name, avatar)
	}
	h.fail(2, "profile-set", "-tenant", "shop", "-username", "lan")
	h.fail(2, "account-groups", "-tenant", "shop", "-username", "lan")
	wantCLIAudits(t, raw, "account_create", 2)
	wantCLIAudits(t, raw, "account_groups_set", 2)
	wantCLIAudits(t, raw, "profile_set", 2)
}

package admincmd_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const (
	shopAPI  = "https://api.shop.example"
	shopIdP  = "https://idp.shop.example"
	bankAPI  = "https://api.bank.example"
	lanSub   = "0901000000"
	minhSub  = "0902000000"
	ghostSub = "0909999999"
)

func linkSubject(t *testing.T, h *harness, realm, sub, id, name string) {
	t.Helper()
	u := store.NewUser{TenantID: "shop", Realm: realm, Sub: sub, CandidateID: id, DisplayName: name, At: time.Now(), By: "test"}
	if linked, err := h.store().LinkUser(context.Background(), u); err != nil || linked.ID != id {
		t.Fatalf("link %s = %+v, %v; want %s", sub, linked, err, id)
	}
}

func newShopRealm(t *testing.T) *harness {
	t.Helper()
	h := newShop(t)
	h.env.Issuer = ownIssuer
	h.run("tenant-create", "-id", "bank", "-name", "Bank")
	if out := h.run("issuer-add", "-issuer", shopAPI, "-tenant", "shop"); out != "issuer "+shopAPI+" trusted for tenant shop, realm "+shopAPI+"\n" {
		t.Fatalf("issuer-add output = %q", out)
	}
	h.run("issuer-add", "-issuer", bankAPI, "-tenant", "bank")
	if out := h.run("issuer-add", "-issuer", shopIdP, "-tenant", "shop", "-realm", shopAPI); out != "issuer "+shopIdP+" trusted for tenant shop, realm "+shopAPI+"\n" {
		t.Fatalf("issuer-add -realm output = %q", out)
	}
	linkSubject(t, h, shopAPI, lanSub, lanID, "lan")
	linkSubject(t, h, shopAPI, minhSub, minhID, "minh")
	return h
}

func TestIssuerAddRefusesTheRealmOfAnotherTenant(t *testing.T) {
	h := newShopRealm(t)
	h.fail(1, "issuer-add", "-issuer", "https://x.shop.example", "-tenant", "shop", "-realm", bankAPI)
	h.fail(1, "issuer-add", "-issuer", "https://y.shop.example", "-tenant", "shop", "-realm", "ghost-realm")
	issuers, err := h.store().TrustedIssuers(context.Background())
	if err != nil || len(issuers) != 3 {
		t.Fatalf("TrustedIssuers = %+v, %v; want only the three added", issuers, err)
	}
}

func TestUserCommandsByIssuerAndSubject(t *testing.T) {
	h := newShopRealm(t)
	h.run("group-create", "-tenant", "shop", "-id", "sales", "-name", "Sales")
	if out := h.run("user-find", "-issuer", shopIdP, "-sub", lanSub); out != "shop "+lanID+"\n" {
		t.Fatalf("user-find output = %q", out)
	}
	if out := h.run("user-disable", "-issuer", shopIdP, "-sub", lanSub); out != "user "+lanID+": user_disable\n" {
		t.Fatalf("user-disable output = %q", out)
	}
	if lan, minh := h.stateOf("users", "shop│"+lanID), h.stateOf("users", "shop│"+minhID); lan != store.StateDisabled || minh != store.StateActive {
		t.Fatalf("states lan %d minh %d; want only lan disabled", lan, minh)
	}
	h.run("user-enable", "-issuer", shopAPI, "-sub", lanSub)
	if lan := h.stateOf("users", "shop│"+lanID); lan != store.StateActive {
		t.Fatalf("lan state = %d after enable; want active", lan)
	}
	h.run("user-set", "-issuer", shopAPI, "-sub", minhSub, "-name", "Minh T.")
	if name, _ := profileOf(t, h, minhID); name != "Minh T." {
		t.Fatalf("minh name = %q; want Minh T.", name)
	}
	h.run("user-groups", "-issuer", shopIdP, "-sub", minhSub, "-groups", "sales")
	if got := userGroupsOf(t, h, minhID); !slices.Equal(got, []string{"sales"}) {
		t.Fatalf("minh groups = %v; want sales", got)
	}
}

func TestUserCommandsByIssuerAndSubjectRefuseBadTargets(t *testing.T) {
	h := newShopRealm(t)
	for _, args := range [][]string{
		{"user-find", "-issuer", "https://ghost.example", "-sub", lanSub},
		{"user-find", "-issuer", shopAPI, "-sub", ghostSub},
		{"user-find", "-issuer", bankAPI, "-sub", lanSub},
		{"user-disable", "-issuer", shopAPI, "-sub", ghostSub},
	} {
		if logs := h.fail(1, args...); !strings.Contains(logs, "not found") || strings.Contains(logs, ghostSub) {
			t.Fatalf("Run(%q) logs = %s; want not found without the subject", args, logs)
		}
	}
	h.fail(2, "user-find", "-issuer", shopAPI, "-sub", "09 01")
	h.fail(2, "user-find", "-issuer", shopAPI)
	h.fail(2, "user-disable")
	h.fail(2, "user-disable", "-tenant", "shop", "-user", lanID, "-issuer", shopAPI, "-sub", lanSub)
	if lan := h.stateOf("users", "shop│"+lanID); lan != store.StateActive {
		t.Fatalf("lan state = %d after refused commands; want active", lan)
	}
}

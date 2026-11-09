package admincmd_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const shopIssuer = "https://shop.example"

func trustedKeyIDs(t *testing.T, h *harness) []string {
	t.Helper()
	keys, err := h.store().TrustedKeys(context.Background())
	if err != nil {
		t.Fatalf("TrustedKeys: %v", err)
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.Issuer+"/"+k.KeyID+"/"+k.Algorithm)
	}
	return ids
}

func issuerState(t *testing.T, h *harness) store.State {
	t.Helper()
	issuers, err := h.store().TrustedIssuers(context.Background())
	if err != nil || len(issuers) != 1 {
		t.Fatalf("TrustedIssuers = %+v, %v; want one", issuers, err)
	}
	return issuers[0].IssuerState
}

func TestIssuerCommands(t *testing.T) {
	h := newShop(t)
	h.env.Issuer = ownIssuer
	h.fail(1, "issuer-add", "-issuer", shopIssuer, "-tenant", "blog")
	h.fail(1, "issuer-add", "-issuer", ownIssuer, "-tenant", "shop")
	if out := h.run("issuer-add", "-issuer", shopIssuer, "-tenant", "shop"); out != "issuer "+shopIssuer+" trusted for tenant shop, realm "+shopIssuer+"\n" {
		t.Fatalf("issuer-add output = %q", out)
	}
	h.fail(1, "issuer-add", "-issuer", shopIssuer, "-tenant", "shop")
	h.fail(1, "issuer-key-add", "-issuer", shopIssuer, "-kid", "shop-1", "-alg", "RS256", "-jwk", rsaJWK(1024))
	h.fail(1, "issuer-key-add", "-issuer", shopIssuer, "-kid", "shop-1", "-alg", "EdDSA", "-jwk", privateEdDSAJWK)
	if ids := trustedKeyIDs(t, h); len(ids) != 0 {
		t.Fatalf("keys after refused adds = %v, want none", ids)
	}
	h.run("issuer-key-add", "-issuer", shopIssuer, "-kid", "shop-1", "-alg", "EdDSA", "-jwk", devtokenEdDSA)
	h.run("issuer-key-add", "-issuer", shopIssuer, "-kid", "shop-2", "-alg", "ES256", "-jwk", devtokenES256)
	h.fail(1, "issuer-key-add", "-issuer", shopIssuer, "-kid", "shop-1", "-alg", "EdDSA", "-jwk", devtokenEdDSA)
	if ids := trustedKeyIDs(t, h); strings.Join(ids, ",") != shopIssuer+"/shop-1/EdDSA,"+shopIssuer+"/shop-2/ES256" {
		t.Fatalf("TrustedKeys = %v, want shop-1 EdDSA and shop-2 ES256", ids)
	}
	h.run("issuer-disable", "-issuer", shopIssuer)
	if st := issuerState(t, h); st != store.StateDisabled {
		t.Fatalf("issuer state = %d, want disabled", st)
	}
	h.run("issuer-enable", "-issuer", shopIssuer)
	if st := issuerState(t, h); st != store.StateActive {
		t.Fatalf("issuer state = %d, want active", st)
	}
	if out := h.run("issuer-key-remove", "-issuer", shopIssuer, "-kid", "shop-1"); out != "issuer "+shopIssuer+" key shop-1 removed\n" {
		t.Fatalf("issuer-key-remove output = %q", out)
	}
	h.fail(1, "issuer-key-remove", "-issuer", shopIssuer, "-kid", "shop-1")
	if ids := trustedKeyIDs(t, h); len(ids) != 1 || ids[0] != shopIssuer+"/shop-2/ES256" {
		t.Fatalf("TrustedKeys after remove = %v, want only shop-2", ids)
	}
	listing := h.run("issuer-keys", "-issuer", shopIssuer)
	lines := strings.Split(strings.TrimSpace(listing), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "KID") || !strings.Contains(lines[1], "shop-1") || !strings.Contains(lines[1], "removed") ||
		!strings.Contains(lines[2], "shop-2") || !strings.Contains(lines[2], "ES256") || !strings.Contains(lines[2], "active") {
		t.Fatalf("issuer-keys = %q", listing)
	}
	if strings.Contains(listing, "11qYAYKx") || strings.Contains(listing, "F716A2r5") {
		t.Fatalf("issuer-keys printed key material: %q", listing)
	}
	h.fail(1, "issuer-keys", "-issuer", "https://blog.example")
	keys, err := h.store().TrustedKeysOf(context.Background(), shopIssuer)
	if err != nil || len(keys) != 2 || keys[0].CreatedBy != "admin-cli" || keys[1].CreatedBy != "admin-cli" {
		t.Fatalf("TrustedKeysOf = %+v, %v; want two keys created by admin-cli", keys, err)
	}
}

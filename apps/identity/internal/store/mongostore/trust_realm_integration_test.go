package mongostore

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func mustCreateIssuerInRealm(t *testing.T, s *Store, issuer, tenantID, realm string) {
	t.Helper()
	if err := s.CreateTrustedIssuer(t.Context(), issuer, tenantID, realm, itAt, "cli:admin"); err != nil {
		t.Fatalf("CreateTrustedIssuer %s in realm %s: %v", issuer, realm, err)
	}
}

func realmsOf(t *testing.T, s *Store) map[string]string {
	t.Helper()
	issuers, err := s.TrustedIssuers(t.Context())
	if err != nil {
		t.Fatalf("TrustedIssuers: %v", err)
	}
	out := make(map[string]string, len(issuers))
	for _, i := range issuers {
		out[i.Issuer] = i.Realm
	}
	return out
}

func TestCreateTrustedIssuerWritesTheIssuerAsTheDefaultRealm(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateIssuerInRealm(t, s, "shop-backend", "shop", "")
	mustCreateIssuerInRealm(t, s, "shop-idp", "shop", "shop-idp")
	for _, issuer := range []string{"shop-backend", "shop-idp"} {
		if got := readTrustedIssuer(t, s, issuer); got.Realm != issuer {
			t.Fatalf("stored realm of %s = %q; want %q", issuer, got.Realm, issuer)
		}
	}
}

func TestCreateTrustedIssuerJoinsTheRealmOfAnIssuerOfTheSameTenant(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateIssuerInRealm(t, s, "shop-old", "shop", "")
	insertDoc(t, s, trustedIssuersCollection, bson.D{
		{Key: "_id", Value: "shop-legacy"}, {Key: "tenant_id", Value: "shop"}, {Key: "state", Value: store.StateActive},
		{Key: "ver", Value: int64(1)}, {Key: "created_at", Value: itAt}, {Key: "updated_at", Value: itAt}, {Key: "updated_by", Value: "cli:admin"},
	})
	mustCreateIssuerInRealm(t, s, "shop-new", "shop", "shop-old")
	mustCreateIssuerInRealm(t, s, "shop-newer", "shop", "shop-legacy")
	want := map[string]string{"shop-old": "shop-old", "shop-legacy": "shop-legacy", "shop-new": "shop-old", "shop-newer": "shop-legacy"}
	got := realmsOf(t, s)
	for issuer, realm := range want {
		if got[issuer] != realm {
			t.Fatalf("realms = %v; want %v", got, want)
		}
	}
	if stored := readTrustedIssuer(t, s, "shop-new"); stored.Realm != "shop-old" {
		t.Fatalf("stored realm of shop-new = %q; want shop-old", stored.Realm)
	}
	mustCreateIssuerInRealm(t, s, "shop-third", "shop", "shop-old")
	if got := realmsOf(t, s)["shop-third"]; got != "shop-old" {
		t.Fatalf("realm of shop-third = %q; want shop-old", got)
	}
}

func TestCreateTrustedIssuerRefusesAnUnknownRealm(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateTenant(t, s, "bank")
	mustCreateIssuerInRealm(t, s, "shop-old", "shop", "")
	mustCreateIssuerInRealm(t, s, "shop-new", "shop", "shop-old")
	mustCreateIssuerInRealm(t, s, "bank-backend", "bank", "")
	for name, realm := range map[string]string{
		"realm of nobody":                          "ghost",
		"realm of another tenant":                  "bank-backend",
		"issuer that sits in another issuer realm": "shop-new",
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.CreateTrustedIssuer(t.Context(), "shop-x", "shop", realm, itAt, "cli:admin"); !errors.Is(err, apperr.ErrNotFound) {
				t.Fatalf("realm %s = %v; want not found", realm, err)
			}
		})
	}
	if err := s.CreateTrustedIssuer(t.Context(), "shop-x", "shop", "shop│old", itAt, "cli:admin"); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("realm with the separator = %v; want invalid argument", err)
	}
	if _, ok := realmsOf(t, s)["shop-x"]; ok {
		t.Fatal("shop-x was written after a refused realm")
	}
}

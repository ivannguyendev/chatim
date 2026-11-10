package mongostore

import (
	"errors"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const resolvedUserID = "a1aaaaaaaaaaaaaaaaaaaaaaaa"

func linkUserInRealm(t *testing.T, s *Store, tenantID, realm, sub, id string) {
	t.Helper()
	u := store.NewUser{TenantID: tenantID, Realm: realm, Sub: sub, CandidateID: id, DisplayName: "Lan", At: itAt, By: jitBy}
	if linked, err := s.LinkUser(t.Context(), u); err != nil || linked.ID != id {
		t.Fatalf("LinkUser %s = %+v, %v; want %s", sub, linked, err, id)
	}
}

func TestResolveUserFindsTheLinkedUserThroughTheIssuerRealm(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateIssuerInRealm(t, s, "shop-old", "shop", "")
	mustCreateIssuerInRealm(t, s, "shop-new", "shop", "shop-old")
	linkUserInRealm(t, s, "shop", "shop-old", itSub, resolvedUserID)
	for _, issuer := range []string{"shop-old", "shop-new"} {
		tenantID, userID, err := s.ResolveUser(t.Context(), issuer, itSub)
		if err != nil || tenantID != "shop" || userID != resolvedUserID {
			t.Fatalf("ResolveUser(%s) = %q, %q, %v; want shop, %s", issuer, tenantID, userID, err, resolvedUserID)
		}
	}
}

func TestResolveUserReadsAnIssuerWithoutARealmFieldAsItsOwnRealm(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	insertDoc(t, s, trustedIssuersCollection, bson.D{
		{Key: "_id", Value: "shop-legacy"}, {Key: "tenant_id", Value: "shop"}, {Key: "state", Value: store.StateActive},
		{Key: "ver", Value: int64(1)}, {Key: "created_at", Value: itAt}, {Key: "updated_at", Value: itAt}, {Key: "updated_by", Value: "cli:admin"},
	})
	linkUserInRealm(t, s, "shop", "shop-legacy", itSub, resolvedUserID)
	tenantID, userID, err := s.ResolveUser(t.Context(), "shop-legacy", itSub)
	if err != nil || tenantID != "shop" || userID != resolvedUserID {
		t.Fatalf("ResolveUser = %q, %q, %v; want shop, %s", tenantID, userID, err, resolvedUserID)
	}
}

func TestResolveUserRefusesUnknownIssuersAndSubjects(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateTenant(t, s, "bank")
	mustCreateIssuerInRealm(t, s, "shop-api", "shop", "")
	mustCreateIssuerInRealm(t, s, "bank-api", "bank", "")
	linkUserInRealm(t, s, "shop", "shop-api", itSub, resolvedUserID)
	for name, c := range map[string]struct{ issuer, sub string }{
		"unknown issuer":         {"ghost-api", itSub},
		"unknown sub":            {"shop-api", "0909999999"},
		"issuer of other tenant": {"bank-api", itSub},
	} {
		t.Run(name, func(t *testing.T) {
			tenantID, userID, err := s.ResolveUser(t.Context(), c.issuer, c.sub)
			if !errors.Is(err, apperr.ErrNotFound) || tenantID != "" || userID != "" {
				t.Fatalf("ResolveUser = %q, %q, %v; want not found", tenantID, userID, err)
			}
			if strings.Contains(err.Error(), c.sub) {
				t.Fatalf("error %q names the subject", err)
			}
		})
	}
}

func TestResolveUserRefusesABadSubject(t *testing.T) {
	s := testDatabase(t)
	for name, c := range map[string]struct{ issuer, sub string }{
		"empty sub":           {"shop-api", ""},
		"space in sub":        {"shop-api", "09 01"},
		"separator in sub":    {"shop-api", "a│b"},
		"sub over 255 bytes":  {"shop-api", strings.Repeat("a", 256)},
		"empty issuer":        {"", itSub},
		"separator in issuer": {"shop│api", itSub},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := s.ResolveUser(t.Context(), c.issuer, c.sub); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("ResolveUser = %v; want invalid argument", err)
			}
		})
	}
}

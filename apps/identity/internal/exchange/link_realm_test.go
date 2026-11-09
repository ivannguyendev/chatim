package exchange_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/exchange"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const otherIssuer = "https://login.acme.example"

type issuersSource struct {
	issuers []store.TrustedIssuer
	public  ed25519.PublicKey
}

func (s issuersSource) TrustedIssuers(context.Context) ([]store.TrustedIssuer, error) {
	return s.issuers, nil
}

func (s issuersSource) TrustedKeys(context.Context) ([]store.TrustedKey, error) {
	issuers := make([]string, len(s.issuers))
	for i, iss := range s.issuers {
		issuers[i] = iss.Issuer
	}
	return edKeys(s.public, issuers...)
}

func activeIssuer(issuer, tenant, realm string) store.TrustedIssuer {
	return store.TrustedIssuer{Issuer: issuer, TenantID: tenant, Realm: realm, IssuerState: store.StateActive, TenantState: store.StateActive}
}

func TestLinkKeyIsTheIssuerTenantAndRealm(t *testing.T) {
	for name, tc := range map[string]struct {
		second   store.TrustedIssuer
		wantLink *store.NewUser
		wantTid  string
	}{
		"an issuer with its default realm links its own user": {
			second:   activeIssuer(otherIssuer, "acme", ""),
			wantLink: &store.NewUser{TenantID: "acme", Realm: otherIssuer, Sub: "ha"}, wantTid: "acme",
		},
		"an issuer sharing the realm finds the same user": {
			second: activeIssuer(otherIssuer, "acme", acmeIssuer), wantTid: "acme",
		},
		"an issuer of another tenant never gets this tenant's user": {
			second:   activeIssuer(otherIssuer, "beta", ""),
			wantLink: &store.NewUser{TenantID: "beta", Realm: otherIssuer, Sub: "ha"}, wantTid: "beta",
		},
	} {
		t.Run(name, func(t *testing.T) {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			v := loadedVerifier(t, issuersSource{issuers: []store.TrustedIssuer{activeIssuer(acmeIssuer, "acme", ""), tc.second}, public: public})
			h := newHarness(t, func(d *exchange.Deps) { d.Verifier = v })
			h.cache("Hà", "", store.StateActive)
			first, err := h.exchange(t, tenantToken(t, private, acmeIssuer, "ha", "chatim"))
			if err != nil || h.parse(t, first.AccessToken).Subject != knownID || len(h.profiles.links) != 0 {
				t.Fatalf("first issuer: Exchange err %v, links %+v; want the cached user %s", err, h.profiles.links, knownID)
			}
			issued, err := h.exchange(t, tenantToken(t, private, otherIssuer, "ha", "chatim"))
			if err != nil {
				t.Fatalf("second issuer: Exchange: %v", err)
			}
			c := h.parse(t, issued.AccessToken)
			if tc.wantLink == nil {
				if c.Subject != knownID || c.Tenant != tc.wantTid || len(h.profiles.links) != 0 {
					t.Fatalf("second issuer: tid=%q sub=%q links=%+v; want %s, %s and no link", c.Tenant, c.Subject, h.profiles.links, tc.wantTid, knownID)
				}
				return
			}
			if len(h.profiles.links) != 1 {
				t.Fatalf("links = %+v; want one", h.profiles.links)
			}
			got := h.profiles.links[0]
			if got.TenantID != tc.wantLink.TenantID || got.Realm != tc.wantLink.Realm || got.Sub != tc.wantLink.Sub {
				t.Fatalf("link key = %s/%s/%s; want %s/%s/%s", got.TenantID, got.Realm, got.Sub, tc.wantLink.TenantID, tc.wantLink.Realm, tc.wantLink.Sub)
			}
			if c.Tenant != tc.wantTid || c.Subject == knownID || c.Subject != got.CandidateID {
				t.Fatalf("second issuer: tid=%q sub=%q; want %s and the new candidate %s", c.Tenant, c.Subject, tc.wantTid, got.CandidateID)
			}
		})
	}
}

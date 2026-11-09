package trust_test

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

const oldAcmeIssuer = "https://old-idp.acme.example"

func sourceWithAcmeRealm(realm string) *fakeSource {
	src := newSource()
	for i := range src.issuers {
		if src.issuers[i].Issuer == acmeIssuer {
			src.issuers[i].Realm = realm
		}
	}
	return src
}

func TestVerifyFillsTheRealmOfTheIssuer(t *testing.T) {
	for name, tc := range map[string]struct {
		realm string
		want  string
	}{
		"issuer without a realm": {realm: "", want: acmeIssuer},
		"issuer with a realm":    {realm: oldAcmeIssuer, want: oldAcmeIssuer},
	} {
		t.Run(name, func(t *testing.T) {
			v := loaded(t, sourceWithAcmeRealm(tc.realm))
			got, err := v.Verify(signEd(t, baseClaims()))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			want := trust.Subject{Tenant: "acme", User: "ha", Issuer: acmeIssuer, Realm: tc.want, KeyID: "ed1"}
			if got != want {
				t.Fatalf("subject = %+v; want %+v", got, want)
			}
		})
	}
}

func TestVerifyComparesTidWithTheTenantNotTheRealm(t *testing.T) {
	v := loaded(t, sourceWithAcmeRealm(oldAcmeIssuer))
	got, err := v.Verify(signEd(t, withClaims(func(c jwt.MapClaims) { c["tid"] = "acme" })))
	if err != nil || got.Tenant != "acme" || got.Realm != oldAcmeIssuer {
		t.Fatalf("tid of the tenant = %+v, %v; want accepted in realm %s", got, err, oldAcmeIssuer)
	}
	for name, tid := range map[string]string{"tid of the realm": oldAcmeIssuer, "tid of another tenant": "beta"} {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(signEd(t, withClaims(func(c jwt.MapClaims) { c["tid"] = tid })))
			if r := trust.ReasonOf(err); r != trust.ReasonTenantMismatch {
				t.Fatalf("reason = %q (%v); want %q", r, err, trust.ReasonTenantMismatch)
			}
		})
	}
}

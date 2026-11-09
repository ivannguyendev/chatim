package trust_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

type refusalCase struct {
	name   string
	token  func(t *testing.T) string
	reason trust.Reason
}

func refusalCases() []refusalCase {
	k := testKeys()
	return []refusalCase{
		{"alg none", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, "ed1", baseClaims())
		}, trust.ReasonAlgorithm},
		{"hs256 keyed by the public jwk", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodHS256, edJWK(k.ed), "ed1", baseClaims())
		}, trust.ReasonAlgorithm},
		{"es256 key with rs256 header", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodRS256, k.rsa, "ec1", baseClaims())
		}, trust.ReasonAlgorithm},
		{"eddsa key with es256 header", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodES256, k.ec, "ed1", baseClaims())
		}, trust.ReasonAlgorithm},
		{"missing kid", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodEdDSA, k.ed, "", baseClaims())
		}, trust.ReasonHeader},
		{"numeric kid", func(t *testing.T) string {
			return withHeader(t, map[string]any{"alg": "EdDSA", "kid": 7}, baseClaims())
		}, trust.ReasonHeader},
		{"crit header", func(t *testing.T) string {
			return withHeader(t, map[string]any{"alg": "EdDSA", "kid": "ed1", "crit": []string{"exp"}}, baseClaims())
		}, trust.ReasonHeader},
		{"not a jwt", func(*testing.T) string { return "abc" }, trust.ReasonHeader},
		{"unknown kid", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodEdDSA, k.ed, "nope", baseClaims())
		}, trust.ReasonKey},
		{"kid of another issuer", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["iss"] = betaIssuer }))
		}, trust.ReasonKey},
		{"unknown issuer", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["iss"] = "https://evil.example" }))
		}, trust.ReasonIssuer},
		{"key of an unknown issuer", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodEdDSA, k.off, "g1", withClaims(func(c jwt.MapClaims) { c["iss"] = "https://ghost.example" }))
		}, trust.ReasonIssuer},
		{"active key under a disabled issuer", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodEdDSA, k.off, "off1", withClaims(func(c jwt.MapClaims) { c["iss"] = offIssuer }))
		}, trust.ReasonIssuer},
		{"missing iss", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { delete(c, "iss") }))
		}, trust.ReasonIssuer},
		{"iss array", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["iss"] = []string{acmeIssuer} }))
		}, trust.ReasonIssuer},
		{"iss number", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["iss"] = 42 }))
		}, trust.ReasonIssuer},
		{"tenant disabled", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodEdDSA, k.frozen, "fz1", withClaims(func(c jwt.MapClaims) { c["iss"] = frozenIssuer }))
		}, trust.ReasonTenantDisabled},
		{"payload changed after signing", func(t *testing.T) string {
			return swapPayload(signEd(t, baseClaims()), withClaims(func(c jwt.MapClaims) { c["sub"] = "hb" }))
		}, trust.ReasonSignature},
		{"signed by another eddsa key", func(t *testing.T) string {
			return signWith(t, jwt.SigningMethodEdDSA, k.beta, "ed1", baseClaims())
		}, trust.ReasonSignature},
		{"wrong aud", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["aud"] = "billing" }))
		}, trust.ReasonAudience},
		{"missing aud", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { delete(c, "aud") }))
		}, trust.ReasonAudience},
		{"aud array of a number", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["aud"] = []any{1} }))
		}, trust.ReasonAudience},
		{"expired beyond leeway", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) {
				c["iat"] = testNow.Add(-5 * time.Minute).Unix()
				c["exp"] = testNow.Add(-31 * time.Second).Unix()
			}))
		}, trust.ReasonTime},
		{"missing exp", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { delete(c, "exp") }))
		}, trust.ReasonTime},
		{"exp string", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["exp"] = "soon" }))
		}, trust.ReasonTime},
		{"iat in the future beyond leeway", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["iat"] = testNow.Add(31 * time.Second).Unix() }))
		}, trust.ReasonTime},
		{"missing iat", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { delete(c, "iat") }))
		}, trust.ReasonTime},
		{"nbf in the future", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["nbf"] = testNow.Add(time.Minute).Unix() }))
		}, trust.ReasonTime},
		{"lives eleven minutes", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["exp"] = testNow.Add(11 * time.Minute).Unix() }))
		}, trust.ReasonLifetime},
		{"exp before iat", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) {
				c["iat"] = testNow.Add(20 * time.Second).Unix()
				c["exp"] = testNow.Add(10 * time.Second).Unix()
			}))
		}, trust.ReasonLifetime},
		{"tid of another tenant", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["tid"] = "beta" }))
		}, trust.ReasonTenantMismatch},
		{"tid number", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["tid"] = 1 }))
		}, trust.ReasonTenantMismatch},
		{"sub number", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { c["sub"] = 5 }))
		}, trust.ReasonSubject},
		{"missing sub", func(t *testing.T) string {
			return signEd(t, withClaims(func(c jwt.MapClaims) { delete(c, "sub") }))
		}, trust.ReasonSubject},
	}
}

func TestVerifyRefusesEveryBadTokenWithOneMessage(t *testing.T) {
	v := loaded(t, newSource())
	var message string
	for _, tc := range refusalCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := v.Verify(tc.token(t))
			if !errors.Is(err, trust.ErrRefused) {
				t.Fatalf("err = %v; want ErrRefused", err)
			}
			if r := trust.ReasonOf(err); r != tc.reason {
				t.Fatalf("reason = %q; want %q", r, tc.reason)
			}
			if got != (trust.Subject{}) {
				t.Fatalf("subject = %+v; want zero", got)
			}
			if message == "" {
				message = err.Error()
			}
			if err.Error() != message || err.Error() != trust.ErrRefused.Error() {
				t.Fatalf("Error() = %q; want %q for every refusal", err.Error(), message)
			}
		})
	}
}

func TestVerifyAcceptsEachAlgorithmAndTakesTheTenantFromTheIssuer(t *testing.T) {
	v := loaded(t, newSource())
	k := testKeys()
	for _, tc := range []struct {
		kid    string
		method jwt.SigningMethod
		key    any
	}{
		{"ed1", jwt.SigningMethodEdDSA, k.ed},
		{"ec1", jwt.SigningMethodES256, k.ec},
		{"rs1", jwt.SigningMethodRS256, k.rsa},
	} {
		t.Run(tc.kid, func(t *testing.T) {
			got, err := v.Verify(signWith(t, tc.method, tc.key, tc.kid, withClaims(func(c jwt.MapClaims) { c["tid"] = "acme" })))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			want := trust.Subject{Tenant: "acme", User: "ha", Issuer: acmeIssuer, Realm: acmeIssuer, KeyID: tc.kid}
			if got != want {
				t.Fatalf("subject = %+v; want %+v", got, want)
			}
		})
	}
}

func TestVerifyAcceptsEdgeCases(t *testing.T) {
	v := loaded(t, newSource())
	for name, claims := range map[string]jwt.MapClaims{
		"aud array holding chatim": withClaims(func(c jwt.MapClaims) { c["aud"] = []string{"billing", audience} }),
		"exp inside leeway": withClaims(func(c jwt.MapClaims) {
			c["iat"] = testNow.Add(-5 * time.Minute).Unix()
			c["exp"] = testNow.Add(-29 * time.Second).Unix()
		}),
		"iat inside leeway":        withClaims(func(c jwt.MapClaims) { c["iat"] = testNow.Add(29 * time.Second).Unix() }),
		"lives exactly ten minute": withClaims(func(c jwt.MapClaims) { c["exp"] = testNow.Add(10 * time.Minute).Unix() }),
		"sub of 64 bytes":          withClaims(func(c jwt.MapClaims) { c["sub"] = strings.Repeat("a", 64) }),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(signEd(t, claims)); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestVerifyReportsNameAndPictureOnlyWhenTheyAreStrings(t *testing.T) {
	v := loaded(t, newSource())
	got, err := v.Verify(signEd(t, withClaims(func(c jwt.MapClaims) {
		c["name"] = "Hà"
		c["picture"] = "https://cdn.example/ha.png"
	})))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !got.HasName || got.Name != "Hà" || !got.HasPicture || got.Picture != "https://cdn.example/ha.png" {
		t.Fatalf("subject = %+v; want name and picture", got)
	}
	got, err = v.Verify(signEd(t, withClaims(func(c jwt.MapClaims) {
		c["name"] = 3
		c["picture"] = []string{"x"}
	})))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.HasName || got.HasPicture || got.Name != "" || got.Picture != "" {
		t.Fatalf("subject = %+v; want no name and no picture", got)
	}
}

func TestVerifyBeforeTheFirstLoadIsUnavailable(t *testing.T) {
	v := newVerifier(t, func() time.Time { return testNow })
	_, err := v.Verify(signEd(t, baseClaims()))
	if !errors.Is(err, apperr.ErrUnavailable) || errors.Is(err, trust.ErrRefused) {
		t.Fatalf("err = %v; want unavailable, not refused", err)
	}
	if v.Ready() {
		t.Fatal("ready before the first load")
	}
}

func TestNewRefusesBadConfig(t *testing.T) {
	for name, cfg := range map[string]trust.Config{
		"no audience":       {MaxLifetime: time.Minute},
		"no max lifetime":   {Audience: audience},
		"negative leeway":   {Audience: audience, MaxLifetime: time.Minute, Leeway: -time.Second},
		"negative lifetime": {Audience: audience, MaxLifetime: -time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := trust.New(cfg); err == nil {
				t.Fatal("New accepted a bad config")
			}
		})
	}
}

func TestLoadKeepsTheOldSnapshotWhenTheSourceFails(t *testing.T) {
	src := newSource()
	fresh := newVerifier(t, func() time.Time { return testNow })
	src.update(func(f *fakeSource) { f.err = errSource })
	if _, err := fresh.Load(context.Background(), src); !errors.Is(err, errSource) {
		t.Fatalf("Load err = %v; want the source error", err)
	}
	if fresh.Ready() || fresh.Stats().ReloadErrors() != 1 {
		t.Fatalf("ready %v reload errors %d; want false and 1", fresh.Ready(), fresh.Stats().ReloadErrors())
	}
	src.update(func(f *fakeSource) { f.err = nil })
	v := loaded(t, src)
	src.update(func(f *fakeSource) { f.err = errSource })
	src.removeKey(acmeIssuer, "ed1")
	if _, err := v.Load(context.Background(), src); !errors.Is(err, errSource) {
		t.Fatalf("Load err = %v; want the source error", err)
	}
	if !v.Ready() || v.Stats().ReloadErrors() != 1 {
		t.Fatalf("ready %v reload errors %d; want true and 1", v.Ready(), v.Stats().ReloadErrors())
	}
	if _, err := v.Verify(signEd(t, baseClaims())); err != nil {
		t.Fatalf("old snapshot lost: %v", err)
	}
}

func TestLoadSkipsABadKeyWithoutBlockingTheOthers(t *testing.T) {
	src := newSource()
	src.update(func(f *fakeSource) {
		f.keys = append(f.keys,
			activeKey(acmeIssuer, "bad1", "EdDSA", []byte(`{"kty":"OKP","crv":"Ed25519","x":"short"}`)),
			activeKey(acmeIssuer, "bad2", "HS256", edJWK(testKeys().ed)),
		)
	})
	v := newVerifier(t, func() time.Time { return testNow })
	res, err := v.Load(context.Background(), src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(res.Skipped) != 2 || res.Skipped[0] != (trust.KeyRef{Issuer: acmeIssuer, KeyID: "bad1"}) {
		t.Fatalf("skipped = %+v; want bad1 and bad2", res.Skipped)
	}
	if !v.Ready() || v.Stats().ReloadErrors() != 2 {
		t.Fatalf("ready %v reload errors %d; want true and 2", v.Ready(), v.Stats().ReloadErrors())
	}
	if _, err := v.Verify(signEd(t, baseClaims())); err != nil {
		t.Fatalf("good key refused: %v", err)
	}
	_, err = v.Verify(signWith(t, jwt.SigningMethodEdDSA, testKeys().ed, "bad1", baseClaims()))
	if trust.ReasonOf(err) != trust.ReasonKey {
		t.Fatalf("bad key reason = %q; want key", trust.ReasonOf(err))
	}
}

func TestLoadDropsARemovedKey(t *testing.T) {
	src := newSource()
	v := loaded(t, src)
	src.removeKey(acmeIssuer, "ed1")
	if _, err := v.Load(context.Background(), src); err != nil {
		t.Fatalf("Load: %v", err)
	}
	_, err := v.Verify(signEd(t, baseClaims()))
	if trust.ReasonOf(err) != trust.ReasonKey {
		t.Fatalf("reason = %q; want key", trust.ReasonOf(err))
	}
}

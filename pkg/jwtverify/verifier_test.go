package jwtverify_test

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

func TestVerifyAcceptsAValidToken(t *testing.T) {
	key := newTestKey(t, testKID)
	v := newVerifier(t, newTestKey(t, "other"), key)
	got, err := v.Verify(signEdDSA(t, key, validClaims()))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := jwtverify.Claims{Tenant: "acme", User: "Alice_1", ExpiresAt: testNow.Add(time.Hour)}
	if got.Tenant != want.Tenant || got.User != want.User || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("Verify = %+v; want %+v", got, want)
	}
}

func TestVerifyAcceptsOptionalClaimsAndHeaders(t *testing.T) {
	key := newTestKey(t, testKID)
	v := newVerifier(t, key)
	claims := withClaim("nbf", testNow.Add(10*time.Second).Unix())
	claims["aud"] = []string{"other", testAudience}
	token := signWith(t, jwt.SigningMethodEdDSA, key.priv, map[string]any{"kid": key.kid, "typ": nil}, claims)
	if _, err := v.Verify(token); err != nil {
		t.Fatalf("Verify without typ, nbf inside leeway, audience list: %v", err)
	}
	expiredInsideLeeway := withClaim("exp", testNow.Add(-20*time.Second).Unix())
	if _, err := v.Verify(signEdDSA(t, key, expiredInsideLeeway)); err != nil {
		t.Fatalf("Verify expired inside leeway: %v", err)
	}
	lowerTyp := signWith(t, jwt.SigningMethodEdDSA, key.priv, map[string]any{"kid": key.kid, "typ": "jwt"}, validClaims())
	if _, err := v.Verify(lowerTyp); err != nil {
		t.Fatalf("Verify typ jwt: %v", err)
	}
}

func TestVerifyRefusesBadTokens(t *testing.T) {
	key := newTestKey(t, testKID)
	stranger := newTestKey(t, testKID)
	v := newVerifier(t, key)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	kid := map[string]any{"kid": testKID}
	cases := []struct {
		name  string
		token string
		want  jwtverify.Reason
	}{
		{"empty", "", jwtverify.ReasonMalformed},
		{"garbage", "a.b.c", jwtverify.ReasonMalformed},
		{"alg none", signWith(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, kid, validClaims()), jwtverify.ReasonAlgorithm},
		{"alg HS256", signWith(t, jwt.SigningMethodHS256, []byte("0123456789abcdef0123456789abcdef"), kid, validClaims()), jwtverify.ReasonAlgorithm},
		{"alg RS256", signWith(t, jwt.SigningMethodRS256, rsaKey, kid, validClaims()), jwtverify.ReasonAlgorithm},
		{"wrong signature", signEdDSA(t, stranger, validClaims()), jwtverify.ReasonSignature},
		{"missing kid", signWith(t, jwt.SigningMethodEdDSA, key.priv, nil, validClaims()), jwtverify.ReasonKeyID},
		{"unknown kid", signWith(t, jwt.SigningMethodEdDSA, key.priv, map[string]any{"kid": "dev-2"}, validClaims()), jwtverify.ReasonKeyID},
		{"numeric kid", signWith(t, jwt.SigningMethodEdDSA, key.priv, map[string]any{"kid": 1}, validClaims()), jwtverify.ReasonKeyID},
		{"typ JOSE", signWith(t, jwt.SigningMethodEdDSA, key.priv, map[string]any{"kid": testKID, "typ": "at+jwt"}, validClaims()), jwtverify.ReasonType},
		{"numeric typ", signWith(t, jwt.SigningMethodEdDSA, key.priv, map[string]any{"kid": testKID, "typ": 1}, validClaims()), jwtverify.ReasonType},
		{"wrong iss", signEdDSA(t, key, withClaim("iss", "evil")), jwtverify.ReasonIssuer},
		{"missing iss", signEdDSA(t, key, withClaim("iss", nil)), jwtverify.ReasonMissingClaim},
		{"wrong aud", signEdDSA(t, key, withClaim("aud", "other")), jwtverify.ReasonAudience},
		{"missing aud", signEdDSA(t, key, withClaim("aud", nil)), jwtverify.ReasonMissingClaim},
		{"missing exp", signEdDSA(t, key, withClaim("exp", nil)), jwtverify.ReasonMissingClaim},
		{"expired past leeway", signEdDSA(t, key, withClaim("exp", testNow.Add(-31*time.Second).Unix())), jwtverify.ReasonExpired},
		{"nbf in the future", signEdDSA(t, key, withClaim("nbf", testNow.Add(time.Minute).Unix())), jwtverify.ReasonNotYetValid},
		{"missing sub", signEdDSA(t, key, withClaim("sub", nil)), jwtverify.ReasonSubject},
		{"empty sub", signEdDSA(t, key, withClaim("sub", "")), jwtverify.ReasonSubject},
		{"sub with dot", signEdDSA(t, key, withClaim("sub", "al.ice")), jwtverify.ReasonSubject},
		{"sub with star", signEdDSA(t, key, withClaim("sub", "al*ce")), jwtverify.ReasonSubject},
		{"sub with gt", signEdDSA(t, key, withClaim("sub", "al>ce")), jwtverify.ReasonSubject},
		{"numeric sub", signEdDSA(t, key, withClaim("sub", 7)), jwtverify.ReasonMalformed},
		{"missing tid", signEdDSA(t, key, withClaim("tid", nil)), jwtverify.ReasonTenant},
		{"empty tid", signEdDSA(t, key, withClaim("tid", "")), jwtverify.ReasonTenant},
		{"tid with dot", signEdDSA(t, key, withClaim("tid", "ac.me")), jwtverify.ReasonTenant},
		{"tid with star", signEdDSA(t, key, withClaim("tid", "ac*me")), jwtverify.ReasonTenant},
		{"tid with gt", signEdDSA(t, key, withClaim("tid", "ac>me")), jwtverify.ReasonTenant},
		{"numeric tid", signEdDSA(t, key, withClaim("tid", 7)), jwtverify.ReasonMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := v.Verify(tc.token)
			if !errors.Is(err, jwtverify.ErrInvalidToken) {
				t.Fatalf("Verify err = %v; want ErrInvalidToken", err)
			}
			if reason := jwtverify.ReasonOf(err); reason != tc.want {
				t.Fatalf("reason = %q (%v); want %q", reason, err, tc.want)
			}
			if got != (jwtverify.Claims{}) {
				t.Fatalf("claims = %+v; want zero", got)
			}
			if tc.token != "" && strings.Contains(err.Error(), tc.token) {
				t.Fatalf("error %q contains the token", err)
			}
			for part := range strings.SplitSeq(tc.token, ".") {
				if len(part) > 8 && strings.Contains(err.Error(), part) {
					t.Fatalf("error %q contains a token part", err)
				}
			}
		})
	}
}

func TestReasonOfIgnoresOtherErrors(t *testing.T) {
	if got := jwtverify.ReasonOf(errors.New("x")); got != "" {
		t.Fatalf("ReasonOf = %q; want empty", got)
	}
	if got := jwtverify.ReasonOf(nil); got != "" {
		t.Fatalf("ReasonOf(nil) = %q; want empty", got)
	}
}

func TestVerifyIsSafeForConcurrentUse(t *testing.T) {
	key := newTestKey(t, testKID)
	v := newVerifier(t, key)
	good := signEdDSA(t, key, validClaims())
	bad := signEdDSA(t, key, withClaim("iss", "evil"))
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			token, wantErr := good, false
			if i%2 == 1 {
				token, wantErr = bad, true
			}
			if _, err := v.Verify(token); (err != nil) != wantErr {
				t.Errorf("Verify = %v; want error %v", err, wantErr)
			}
		})
	}
	wg.Wait()
}

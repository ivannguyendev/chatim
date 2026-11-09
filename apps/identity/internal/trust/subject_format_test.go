package trust_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

func TestVerifyAcceptsAnyPrintableASCIISubUpTo255Bytes(t *testing.T) {
	v := loaded(t, newSource())
	for name, sub := range map[string]string{
		"auth0 style":       "auth0|abc",
		"email":             "Lan.Nguyen+chat@acme.example",
		"every printable":   "!\"#$%&'()*+,-./0123456789:;<=>?@AZ[\\]^_`az{|}~",
		"single byte":       "x",
		"exactly 255 bytes": strings.Repeat("a", 255),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := v.Verify(signEd(t, withClaims(func(c jwt.MapClaims) { c["sub"] = sub })))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got.User != sub {
				t.Fatalf("User = %q; want %q", got.User, sub)
			}
		})
	}
}

func TestVerifyRefusesASubOutsidePrintableASCII(t *testing.T) {
	v := loaded(t, newSource())
	for name, sub := range map[string]string{
		"empty":         "",
		"256 bytes":     strings.Repeat("a", 256),
		"space":         "lan nguyen",
		"tab":           "lan\tnguyen",
		"delete byte":   "lan\x7f",
		"non-ASCII":     "hà",
		"key separator": "a│b",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := v.Verify(signEd(t, withClaims(func(c jwt.MapClaims) { c["sub"] = sub })))
			if !errors.Is(err, trust.ErrRefused) || trust.ReasonOf(err) != trust.ReasonSubject || got != (trust.Subject{}) {
				t.Fatalf("Verify = %+v, %v (reason %q); want refused for subject", got, err, trust.ReasonOf(err))
			}
		})
	}
}

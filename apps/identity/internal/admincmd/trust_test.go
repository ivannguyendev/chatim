package admincmd_test

import (
	"context"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

const (
	ownIssuer       = "https://chat.example"
	devtokenEdDSA   = `{"kty":"OKP","crv":"Ed25519","kid":"shop-1","alg":"EdDSA","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`
	devtokenES256   = `{"kty":"EC","crv":"P-256","kid":"shop-2","alg":"ES256","x":"F716A2r5TitIaykks7MEVTS-9GAG5E7OCjjBt_mQj6g","y":"7ZqL6fqxg2FmbhxgffVyuI87etMriSm7LnQWm4EFKX4"}`
	privateEdDSAJWK = `{"kty":"OKP","crv":"Ed25519","kid":"shop-1","alg":"EdDSA","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo","d":"nWGxne_9WmC6hEr0kuwsxERJxWl7MmkZcDusAxyuf2A"}`
)

type trustStore struct {
	closedStore
	issuers []store.TrustedIssuer
	created []string
	tenants []string
	states  []store.State
	added   []store.TrustedKey
	writers []string
}

func (s *trustStore) TrustedIssuers(context.Context) ([]store.TrustedIssuer, error) {
	return s.issuers, nil
}

func (s *trustStore) CreateTrustedIssuer(_ context.Context, issuer, tenant string, _ time.Time, by string) error {
	s.created = append(s.created, issuer)
	s.tenants = append(s.tenants, tenant)
	s.writers = append(s.writers, by)
	return nil
}

func (s *trustStore) SetTrustedIssuerState(_ context.Context, _ string, state store.State, _ time.Time, by string) error {
	s.states = append(s.states, state)
	s.writers = append(s.writers, by)
	return nil
}

func (s *trustStore) AddTrustedKey(_ context.Context, k store.TrustedKey, _ time.Time, by string) error {
	s.added = append(s.added, k)
	s.writers = append(s.writers, by)
	return nil
}

func newTrustStore() *trustStore {
	return &trustStore{issuers: []store.TrustedIssuer{{Issuer: "https://shop.example", TenantID: "shop", IssuerState: store.StateActive, TenantState: store.StateActive}}}
}

func trustEnv(t *testing.T, st *trustStore) (admincmd.Env, *strings.Builder) {
	t.Helper()
	env, _, _ := testEnv(t, func(context.Context) (admincmd.Store, error) { return st, nil })
	env.Issuer = ownIssuer
	text := &strings.Builder{}
	env.Out = text
	return env, text
}

func rsaJWK(bits int) string {
	n := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
	n.SetBit(n, 0, 1)
	enc := base64.RawURLEncoding.EncodeToString
	return `{"kty":"RSA","n":"` + enc(n.Bytes()) + `","e":"` + enc(big.NewInt(65537).Bytes()) + `"}`
}

func TestIssuerCommandsAreKnown(t *testing.T) {
	for _, name := range []string{"issuer-add", "issuer-disable", "issuer-enable", "issuer-key-add", "issuer-key-remove", "issuer-keys"} {
		if !admincmd.Is(name) {
			t.Fatalf("Is(%q) = false", name)
		}
	}
}

func TestIssuerCommandUsageExitsWithTwo(t *testing.T) {
	st := newTrustStore()
	env, _ := trustEnv(t, st)
	const iss = "https://shop.example"
	cases := [][]string{
		{"issuer-add", "-issuer", iss},
		{"issuer-add", "-tenant", "shop"},
		{"issuer-add", "-issuer", "https://shop example", "-tenant", "shop"},
		{"issuer-add", "-issuer", "https://shop.example/\x01", "-tenant", "shop"},
		{"issuer-add", "-issuer", strings.Repeat("a", 256), "-tenant", "shop"},
		{"issuer-add", "-issuer", iss, "-tenant", "Bad Tenant"},
		{"issuer-disable"},
		{"issuer-enable", "-tenant", "shop"},
		{"issuer-key-add", "-issuer", iss, "-kid", "shop-1", "-alg", "EdDSA"},
		{"issuer-key-add", "-issuer", iss, "-kid", "shop-1", "-jwk", devtokenEdDSA},
		{"issuer-key-add", "-issuer", iss, "-kid", "shop-1", "-alg", "HS256", "-jwk", devtokenEdDSA},
		{"issuer-key-add", "-issuer", iss, "-kid", "shop 1", "-alg", "EdDSA", "-jwk", devtokenEdDSA},
		{"issuer-key-remove", "-issuer", iss},
		{"issuer-keys"},
		{"issuer-keys", "-issuer", iss, "extra"},
	}
	for _, args := range cases {
		if code := admincmd.Run(context.Background(), env, args); code != 2 {
			t.Fatalf("Run(%q) = %d, want 2", args, code)
		}
	}
	if len(st.created)+len(st.states)+len(st.added) != 0 {
		t.Fatalf("store written on usage errors: %+v", st)
	}
}

func TestIssuerKeyAddRefusesBadKeysWithoutWriting(t *testing.T) {
	cases := map[string][]string{
		"rsa 1024":       {"-alg", "RS256", "-kid", "shop-1", "-jwk", rsaJWK(1024)},
		"private d":      {"-alg", "EdDSA", "-kid", "shop-1", "-jwk", privateEdDSAJWK},
		"alg mismatch":   {"-alg", "ES256", "-kid", "shop-1", "-jwk", devtokenEdDSA},
		"not json":       {"-alg", "EdDSA", "-kid", "shop-1", "-jwk", "{"},
		"kid differs":    {"-alg", "EdDSA", "-kid", "shop-9", "-jwk", devtokenEdDSA},
		"unknown issuer": {"-alg", "EdDSA", "-kid", "shop-1", "-jwk", devtokenEdDSA, "-issuer", "https://blog.example"},
	}
	for name, extra := range cases {
		st := newTrustStore()
		env, out := trustEnv(t, st)
		args := append([]string{"issuer-key-add", "-issuer", "https://shop.example"}, extra...)
		if code := admincmd.Run(context.Background(), env, args); code != 1 {
			t.Fatalf("%s: Run = %d, want 1", name, code)
		}
		if len(st.added) != 0 || out.Len() != 0 {
			t.Fatalf("%s: added %+v, out %q; want nothing written", name, st.added, out)
		}
	}
}

func TestIssuerKeyAddStoresCanonicalDevtokenKeys(t *testing.T) {
	for _, c := range []struct{ alg, kid, jwk string }{
		{trust.AlgorithmEdDSA, "shop-1", devtokenEdDSA},
		{trust.AlgorithmES256, "shop-2", devtokenES256},
		{trust.AlgorithmRS256, "shop-3", rsaJWK(2048)},
	} {
		_, canonical, err := trust.ParsePublicJWK(c.alg, []byte(c.jwk))
		if err != nil {
			t.Fatalf("%s: ParsePublicJWK(devtoken format) = %v", c.alg, err)
		}
		st := newTrustStore()
		env, out := trustEnv(t, st)
		args := []string{"issuer-key-add", "-issuer", "https://shop.example", "-kid", c.kid, "-alg", c.alg, "-jwk", c.jwk}
		if code := admincmd.Run(context.Background(), env, args); code != 0 {
			t.Fatalf("%s: Run = %d, want 0", c.alg, code)
		}
		if len(st.added) != 1 {
			t.Fatalf("%s: added %d keys, want 1", c.alg, len(st.added))
		}
		k := st.added[0]
		if k.Issuer != "https://shop.example" || k.KeyID != c.kid || k.Algorithm != c.alg || string(k.PublicKey) != string(canonical) {
			t.Fatalf("%s: stored %+v, want canonical %s", c.alg, k, canonical)
		}
		if st.writers[0] != "admin-cli" {
			t.Fatalf("%s: written by %q, want admin-cli", c.alg, st.writers[0])
		}
		if want := "issuer https://shop.example key " + c.kid + " added (" + c.alg + ")\n"; out.String() != want {
			t.Fatalf("%s: out = %q, want %q", c.alg, out, want)
		}
	}
}

func TestIssuerAddRefusesOwnIssuer(t *testing.T) {
	st := newTrustStore()
	env, out := trustEnv(t, st)
	if code := admincmd.Run(context.Background(), env, []string{"issuer-add", "-issuer", ownIssuer, "-tenant", "shop"}); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
	if len(st.created) != 0 || out.Len() != 0 {
		t.Fatalf("created %v, out %q; want nothing", st.created, out)
	}
	if code := admincmd.Run(context.Background(), env, []string{"issuer-add", "-issuer", "https://blog.example", "-tenant", "shop"}); code != 0 {
		t.Fatalf("Run other issuer = %d, want 0", code)
	}
	if len(st.created) != 1 || st.created[0] != "https://blog.example" || st.tenants[0] != "shop" || st.writers[0] != "admin-cli" {
		t.Fatalf("created %v for %v by %v", st.created, st.tenants, st.writers)
	}
	if out.String() != "issuer https://blog.example trusted for tenant shop\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestIssuerStateRefusesAnUnknownIssuer(t *testing.T) {
	st := newTrustStore()
	env, out := trustEnv(t, st)
	if code := admincmd.Run(context.Background(), env, []string{"issuer-disable", "-issuer", "https://shop.example"}); code != 0 {
		t.Fatalf("Run = %d, want 0", code)
	}
	if len(st.states) != 1 || st.states[0] != store.StateDisabled || st.writers[0] != "admin-cli" {
		t.Fatalf("states %v by %v", st.states, st.writers)
	}
	if out.String() != "issuer https://shop.example: issuer_disable\n" {
		t.Fatalf("out = %q", out)
	}
	if code := admincmd.Run(context.Background(), env, []string{"issuer-enable", "-issuer", "https://blog.example"}); code != 1 {
		t.Fatalf("Run on an unknown issuer = %d, want 1", code)
	}
	if len(st.states) != 1 {
		t.Fatalf("states %v; want no write for an unknown issuer", st.states)
	}
}

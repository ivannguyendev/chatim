package password_test

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
)

var fastParams = password.Params{Memory: 64, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}

func newHasher(t *testing.T, p password.Params) *password.Hasher {
	t.Helper()
	h, err := password.NewHasher(p, rand.Reader)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return h
}

func TestHashThenVerifyAcceptsTheSamePassword(t *testing.T) {
	h := newHasher(t, fastParams)
	encoded, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("encoded = %q, want PHC argon2id prefix", encoded)
	}
	ok, err := h.Verify(encoded, "correct horse battery")
	if err != nil || !ok {
		t.Fatalf("Verify = %v, %v; want true, nil", ok, err)
	}
}

func TestVerifyRejectsAChangedPassword(t *testing.T) {
	h := newHasher(t, fastParams)
	encoded, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	ok, err := h.Verify(encoded, "correct horse batterz")
	if err != nil || ok {
		t.Fatalf("Verify = %v, %v; want false, nil", ok, err)
	}
}

func TestHashUsesAFreshSaltEachTime(t *testing.T) {
	h := newHasher(t, fastParams)
	first, _ := h.Hash("same password here")
	second, _ := h.Hash("same password here")
	if first == second {
		t.Fatal("two hashes of one password are equal; salt is not random")
	}
}

func TestVerifyReadsParametersFromTheStoredString(t *testing.T) {
	old := newHasher(t, password.Params{Memory: 32, Time: 2, Threads: 1, SaltLength: 16, KeyLength: 32})
	encoded, err := old.Hash("upgrade me please")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	current := newHasher(t, fastParams)
	ok, err := current.Verify(encoded, "upgrade me please")
	if err != nil || !ok {
		t.Fatalf("Verify with other params = %v, %v; want true, nil", ok, err)
	}
	if !current.NeedsRehash(encoded) {
		t.Fatal("NeedsRehash = false for older params, want true")
	}
	fresh, _ := current.Hash("upgrade me please")
	if current.NeedsRehash(fresh) {
		t.Fatal("NeedsRehash = true for current params, want false")
	}
}

func TestVerifyRejectsMalformedStrings(t *testing.T) {
	h := newHasher(t, fastParams)
	good, _ := h.Hash("some password value")
	parts := strings.Split(good, "$")
	cases := map[string]string{
		"empty":         "",
		"bcrypt":        "$2a$10$abcdefghijklmnopqrstuv",
		"missing hash":  strings.Join(parts[:5], "$"),
		"wrong version": strings.Replace(good, "v=19", "v=16", 1),
		"bad salt":      strings.Join([]string{"", parts[1], parts[2], parts[3], "!!!", parts[5]}, "$"),
		"bad key":       strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], "!!!"}, "$"),
		"bad params":    strings.Replace(good, "m=64,t=1,p=1", "m=x,t=1,p=1", 1),
		"huge memory":   strings.Replace(good, "m=64", "m=1073741824", 1),
		"huge time":     strings.Replace(good, "t=1", "t=1000", 1),
		"huge threads":  strings.Replace(good, "p=1", "p=200", 1),
		"zero time":     strings.Replace(good, "t=1", "t=0", 1),
		"short salt":    strings.Join([]string{"", parts[1], parts[2], parts[3], "AAAA", parts[5]}, "$"),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := h.Verify(encoded, "some password value")
			if ok || !errors.Is(err, password.ErrMalformedHash) {
				t.Fatalf("Verify = %v, %v; want false, ErrMalformedHash", ok, err)
			}
		})
	}
}

func TestVerifyDummyNeverPanicsAndTakesAHash(t *testing.T) {
	h := newHasher(t, fastParams)
	h.VerifyDummy("anything at all")
}

func TestNewHasherRejectsInvalidParams(t *testing.T) {
	cases := map[string]password.Params{
		"zero memory":  {Memory: 0, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32},
		"zero time":    {Memory: 64, Time: 0, Threads: 1, SaltLength: 16, KeyLength: 32},
		"zero threads": {Memory: 64, Time: 1, Threads: 0, SaltLength: 16, KeyLength: 32},
		"short salt":   {Memory: 64, Time: 1, Threads: 1, SaltLength: 4, KeyLength: 32},
		"short key":    {Memory: 64, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 8},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := password.NewHasher(p, rand.Reader); err == nil {
				t.Fatal("NewHasher accepted invalid params")
			}
		})
	}
}

func BenchmarkHash(b *testing.B) {
	h, err := password.NewHasher(password.DefaultParams, rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := h.Hash("a reasonably long password"); err != nil {
			b.Fatal(err)
		}
	}
}

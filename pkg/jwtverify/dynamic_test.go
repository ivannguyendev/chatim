package jwtverify_test

import (
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
)

func TestSetDynamicAddsKeysAtRuntime(t *testing.T) {
	file := newTestKey(t, testKID)
	fresh := newTestKey(t, "iam-2")
	v := newVerifier(t, file)
	token := signEdDSA(t, fresh, validClaims())
	if _, err := v.Verify(token); jwtverify.ReasonOf(err) != jwtverify.ReasonKeyID {
		t.Fatalf("Verify before SetDynamic = %v; want unknown kid", err)
	}
	conflicts, err := v.SetDynamic(keySet(t, publicJWK(fresh)))
	if err != nil || conflicts != 0 {
		t.Fatalf("SetDynamic = %d, %v", conflicts, err)
	}
	if _, err := v.Verify(token); err != nil {
		t.Fatalf("Verify after SetDynamic: %v", err)
	}
	if _, err := v.Verify(signEdDSA(t, file, validClaims())); err != nil {
		t.Fatalf("file key stopped working: %v", err)
	}
	if _, err := v.SetDynamic(keySet(t)); err != nil {
		t.Fatalf("SetDynamic empty: %v", err)
	}
	if _, err := v.Verify(token); jwtverify.ReasonOf(err) != jwtverify.ReasonKeyID {
		t.Fatalf("Verify after the key left = %v; want unknown kid", err)
	}
}

func TestSetDynamicKeepsFileKeyOnConflict(t *testing.T) {
	file := newTestKey(t, testKID)
	impostor := newTestKey(t, testKID)
	other := newTestKey(t, "iam-2")
	v := newVerifier(t, file)
	conflicts, err := v.SetDynamic(keySet(t, publicJWK(impostor), publicJWK(other)))
	if err != nil {
		t.Fatalf("SetDynamic: %v", err)
	}
	if conflicts != 1 {
		t.Fatalf("conflicts = %d; want 1", conflicts)
	}
	if _, err := v.Verify(signEdDSA(t, impostor, validClaims())); jwtverify.ReasonOf(err) != jwtverify.ReasonSignature {
		t.Fatalf("Verify with the conflicting key = %v; want bad signature", err)
	}
	if _, err := v.Verify(signEdDSA(t, file, validClaims())); err != nil {
		t.Fatalf("Verify with the file key: %v", err)
	}
	if _, err := v.Verify(signEdDSA(t, other, validClaims())); err != nil {
		t.Fatalf("Verify with the other dynamic key: %v", err)
	}
	same, err := v.SetDynamic(keySet(t, publicJWK(file)))
	if err != nil || same != 0 {
		t.Fatalf("SetDynamic with the file key itself = %d, %v; want 0 conflicts", same, err)
	}
}

func TestSetDynamicKeepsPreviousKeysOnBadInput(t *testing.T) {
	file := newTestKey(t, testKID)
	fresh := newTestKey(t, "iam-2")
	v := newVerifier(t, file)
	if _, err := v.SetDynamic(keySet(t, publicJWK(fresh))); err != nil {
		t.Fatalf("SetDynamic: %v", err)
	}
	if _, err := v.SetDynamic([]byte("{not json")); err == nil {
		t.Fatal("SetDynamic accepted garbage")
	}
	private := publicJWK(fresh)
	private["d"] = b64(fresh.priv.Seed())
	if _, err := v.SetDynamic(keySet(t, private)); err == nil {
		t.Fatal("SetDynamic accepted a private key")
	}
	if _, err := v.Verify(signEdDSA(t, fresh, validClaims())); err != nil {
		t.Fatalf("previous dynamic key lost: %v", err)
	}
}

func TestSetDynamicIsSafeWithConcurrentVerify(t *testing.T) {
	file := newTestKey(t, testKID)
	fresh := newTestKey(t, "iam-2")
	v := newVerifier(t, file)
	token := signEdDSA(t, file, validClaims())
	set := keySet(t, publicJWK(fresh))
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				if _, err := v.Verify(token); err != nil {
					t.Errorf("Verify: %v", err)
				}
			}
		})
	}
	wg.Go(func() {
		for range 50 {
			if _, err := v.SetDynamic(set); err != nil {
				t.Errorf("SetDynamic: %v", err)
			}
		}
	})
	wg.Wait()
}

func TestKeyIDOfReadsTheHeaderWithoutVerifying(t *testing.T) {
	key := newTestKey(t, "iam-9")
	token := signEdDSA(t, key, validClaims())
	if got := jwtverify.KeyIDOf(token); got != "iam-9" {
		t.Fatalf("KeyIDOf = %q; want iam-9", got)
	}
	noKid := signWith(t, jwt.SigningMethodEdDSA, key.priv, nil, validClaims())
	for _, bad := range []string{"", "a.b.c", noKid} {
		if got := jwtverify.KeyIDOf(bad); got != "" {
			t.Fatalf("KeyIDOf(%q) = %q; want empty", bad, got)
		}
	}
}

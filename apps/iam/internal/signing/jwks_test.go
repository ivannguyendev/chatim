package signing_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
)

type jwksDoc struct {
	Keys []map[string]any `json:"keys"`
}

func readKIDs(t *testing.T, data []byte) []string {
	t.Helper()
	var doc jwksDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	kids := make([]string, 0, len(doc.Keys))
	for _, k := range doc.Keys {
		if _, private := k["d"]; private {
			t.Fatal("JWKS contains private key material d")
		}
		kids = append(kids, k["kid"].(string))
	}
	return kids
}

func TestJWKSHasOnlyPublicFields(t *testing.T) {
	k := newKey(t)
	data, err := signing.JWKS([]signing.PublicKey{k.PublicKey()})
	if err != nil {
		t.Fatalf("JWKS: %v", err)
	}
	if got := readKIDs(t, data); len(got) != 1 || got[0] != k.ID {
		t.Fatalf("kids = %v, want [%s]", got, k.ID)
	}
	for _, field := range []string{`"kty":"OKP"`, `"crv":"Ed25519"`, `"alg":"EdDSA"`, `"use":"sig"`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("JWKS %s lacks %s", data, field)
		}
	}
}

func TestMergeKeepsExistingKeysAndAddsNewOnes(t *testing.T) {
	dev, a := newKey(t), newKey(t)
	existing, _ := signing.JWKS([]signing.PublicKey{dev.PublicKey()})
	merged, err := signing.Merge(existing, []signing.PublicKey{a.PublicKey()})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got := readKIDs(t, merged); len(got) != 2 || got[0] != dev.ID || got[1] != a.ID {
		t.Fatalf("kids = %v, want [%s %s]", got, dev.ID, a.ID)
	}
	again, err := signing.Merge(merged, []signing.PublicKey{a.PublicKey()})
	if err != nil {
		t.Fatalf("Merge twice: %v", err)
	}
	if got := readKIDs(t, again); len(got) != 2 {
		t.Fatalf("merging the same key twice gave %v, want 2 keys", got)
	}
}

func TestMergeRejectsAConflictingKeyID(t *testing.T) {
	a, b := newKey(t), newKey(t)
	existing, _ := signing.JWKS([]signing.PublicKey{a.PublicKey()})
	clash := b.PublicKey()
	clash.ID = a.ID
	if _, err := signing.Merge(existing, []signing.PublicKey{clash}); !errors.Is(err, signing.ErrKeyConflict) {
		t.Fatalf("Merge(conflict) = %v, want ErrKeyConflict", err)
	}
}

func TestMergeRejectsPrivateMaterialInTheExistingFile(t *testing.T) {
	k := newKey(t)
	withD := `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"dev","x":"AAAA","d":"BBBB"}]}`
	if _, err := signing.Merge([]byte(withD), []signing.PublicKey{k.PublicKey()}); err == nil {
		t.Fatal("Merge accepted an existing JWKS with private material")
	}
}

func TestMergeAcceptsAnEmptyExistingFile(t *testing.T) {
	k := newKey(t)
	merged, err := signing.Merge(nil, []signing.PublicKey{k.PublicKey()})
	if err != nil {
		t.Fatalf("Merge(nil): %v", err)
	}
	if got := readKIDs(t, merged); len(got) != 1 {
		t.Fatalf("kids = %v, want one", got)
	}
}

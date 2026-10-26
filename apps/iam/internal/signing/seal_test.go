package signing_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
)

func newKEK(t *testing.T) []byte {
	t.Helper()
	kek := make([]byte, signing.KEKLength)
	if _, err := rand.Read(kek); err != nil {
		t.Fatal(err)
	}
	return kek
}

func newSealer(t *testing.T, kek []byte) *signing.Sealer {
	t.Helper()
	s, err := signing.NewSealer(kek, rand.Reader)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestSealThenOpenReturnsTheSameKey(t *testing.T) {
	s := newSealer(t, newKEK(t))
	k := newKey(t)
	sealed, err := s.Seal(k)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, k.Private.Seed()) {
		t.Fatal("sealed bytes contain the plain seed")
	}
	opened, err := s.Open(k.ID, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !opened.Private.Equal(k.Private) || opened.ID != k.ID {
		t.Fatal("opened key differs from the sealed key")
	}
}

func TestOpenFailsWithAnotherKEK(t *testing.T) {
	k := newKey(t)
	sealed, err := newSealer(t, newKEK(t)).Seal(k)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := newSealer(t, newKEK(t)).Open(k.ID, sealed); !errors.Is(err, signing.ErrSealedKey) {
		t.Fatalf("Open with another KEK = %v, want ErrSealedKey", err)
	}
}

func TestOpenFailsWhenTheKeyIDDiffers(t *testing.T) {
	s := newSealer(t, newKEK(t))
	k := newKey(t)
	sealed, err := s.Seal(k)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := s.Open("k-otherkeyid00", sealed); !errors.Is(err, signing.ErrSealedKey) {
		t.Fatalf("Open with another kid = %v, want ErrSealedKey", err)
	}
}

func TestOpenFailsOnTruncatedInput(t *testing.T) {
	s := newSealer(t, newKEK(t))
	if _, err := s.Open("k-abcdefghijkl", []byte{1, 2, 3}); !errors.Is(err, signing.ErrSealedKey) {
		t.Fatalf("Open(truncated) = %v, want ErrSealedKey", err)
	}
}

func TestNewSealerRequiresA32ByteKEK(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := signing.NewSealer(make([]byte, n), rand.Reader); err == nil {
			t.Fatalf("NewSealer accepted a %d-byte KEK", n)
		}
	}
}

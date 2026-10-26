package password_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestPolicyLengthBounds(t *testing.T) {
	p := password.Policy{MinLength: 12}
	cases := map[string]bool{
		strings.Repeat("a", 11):  false,
		strings.Repeat("a", 12):  true,
		strings.Repeat("a", 128): true,
		strings.Repeat("a", 129): false,
		strings.Repeat("é", 12):  true,
		strings.Repeat("é", 11):  false,
	}
	for pw, want := range cases {
		err := p.Check(pw)
		if want && err != nil {
			t.Errorf("Check(%d runes) = %v, want nil", len([]rune(pw)), err)
		}
		if !want && !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("Check(%d runes) = %v, want ErrInvalidArgument", len([]rune(pw)), err)
		}
	}
}

func TestPolicyRejectsInvalidUTF8(t *testing.T) {
	p := password.Policy{MinLength: 12}
	if err := p.Check(strings.Repeat("a", 12) + "\xff"); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Check(invalid utf-8) = %v, want ErrInvalidArgument", err)
	}
}

func TestTemporaryPasswordShapeAndUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		pw, err := password.Temporary(rand.Reader)
		if err != nil {
			t.Fatalf("Temporary: %v", err)
		}
		if len(pw) != password.TemporaryLength {
			t.Fatalf("len = %d, want %d", len(pw), password.TemporaryLength)
		}
		if strings.ContainsAny(pw, "0O1lI") {
			t.Fatalf("temporary password %q has an ambiguous character", pw)
		}
		for _, c := range pw {
			if !strings.ContainsRune(password.TemporaryAlphabet, c) {
				t.Fatalf("temporary password has %q outside the alphabet", c)
			}
		}
		if _, dup := seen[pw]; dup {
			t.Fatalf("duplicate temporary password %q", pw)
		}
		seen[pw] = struct{}{}
	}
}

func TestTemporaryPasswordFailsWhenRandomnessFails(t *testing.T) {
	if _, err := password.Temporary(bytes.NewReader(nil)); err == nil {
		t.Fatal("Temporary with an empty reader returned no error")
	}
}

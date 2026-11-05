package signing_test

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

func TestNewSessionIDIsTwentySixLowercaseBase32CharactersAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id, err := signing.NewSessionID(rand.Reader)
		if err != nil {
			t.Fatalf("NewSessionID: %v", err)
		}
		if len(id) != 26 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz234567") != "" || ids.ValidUser(id) != nil {
			t.Fatalf("id %q is not 26 characters of [a-z2-7]", id)
		}
		if !signing.ValidSessionID(id) {
			t.Fatalf("ValidSessionID(%q) = false for a fresh id", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewSessionIDFailsWhenRandomnessFails(t *testing.T) {
	if _, err := signing.NewSessionID(bytes.NewReader(nil)); err == nil {
		t.Fatal("NewSessionID with an empty reader returned no error")
	}
}

func TestValidSessionIDRejectsWrongLengthAndAlphabet(t *testing.T) {
	good := strings.Repeat("a", 25) + "7"
	if !signing.ValidSessionID(good) {
		t.Fatalf("ValidSessionID(%q) = false", good)
	}
	cases := map[string]string{
		"empty":     "",
		"25 chars":  good[:25],
		"27 chars":  good + "a",
		"uppercase": "A" + good[1:],
		"digit 1":   "1" + good[1:],
		"digit 8":   "8" + good[1:],
		"dash":      "-" + good[1:],
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			if signing.ValidSessionID(id) {
				t.Fatalf("ValidSessionID(%q) = true", id)
			}
		})
	}
}

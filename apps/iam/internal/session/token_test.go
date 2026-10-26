package session_test

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

func TestNewIDIsAValidUserIDAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id, err := session.NewID(rand.Reader)
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if len(id) != session.IDLength || ids.ValidUser(id) != nil || strings.ToLower(id) != id {
			t.Fatalf("id %q is not %d lowercase id characters", id, session.IDLength)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewIDFailsWhenRandomnessFails(t *testing.T) {
	if _, err := session.NewID(bytes.NewReader(nil)); err == nil {
		t.Fatal("NewID with an empty reader returned no error")
	}
}

func TestRefreshTokenRoundTrip(t *testing.T) {
	sid, _ := session.NewID(rand.Reader)
	token, err := session.NewRefreshToken(sid, rand.Reader)
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	got, ok := session.ParseRefreshToken(token)
	if !ok || got != sid {
		t.Fatalf("ParseRefreshToken = %q, %v; want %q, true", got, ok, sid)
	}
	other, _ := session.NewRefreshToken(sid, rand.Reader)
	if other == token {
		t.Fatal("two refresh tokens for one session are equal")
	}
	if bytes.Equal(session.HashToken(token), session.HashToken(other)) {
		t.Fatal("two different tokens hash equally")
	}
}

func TestParseRefreshTokenRejectsMalformedInput(t *testing.T) {
	sid, _ := session.NewID(rand.Reader)
	secret := strings.Repeat("A", session.SecretLength)
	cases := map[string]string{
		"empty":         "",
		"no dot":        sid + secret,
		"two dots":      sid + "." + secret + ".x",
		"bad sid":       "UPPER." + secret,
		"short secret":  sid + "." + secret[:10],
		"bad alphabet":  sid + "." + strings.Repeat("!", session.SecretLength),
		"access token":  "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.c2ln",
		"short sid":     "abc." + secret,
		"oversized sid": strings.Repeat("a", 65) + "." + secret,
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := session.ParseRefreshToken(token); ok {
				t.Fatalf("ParseRefreshToken(%q) accepted malformed input", token)
			}
		})
	}
}

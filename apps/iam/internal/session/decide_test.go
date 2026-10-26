package session_test

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
)

const grace = 30 * time.Second

var start = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

func hashOf(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func liveState() session.State {
	return session.State{
		IdleExpiresAt: start.Add(30 * 24 * time.Hour),
		ExpiresAt:     start.Add(90 * 24 * time.Hour),
		KeyThumbprint: "jkt-lan",
		Current:       hashOf("token-1"),
	}
}

func TestDecideOutcomes(t *testing.T) {
	rotated := session.Advance(liveState(), hashOf("token-2"), "jti-1", start, 30*24*time.Hour)
	cases := []struct {
		name      string
		state     session.State
		presented string
		jkt       string
		now       time.Time
		want      session.Outcome
	}{
		{"current token rotates", liveState(), "token-1", "jkt-lan", start, session.Rotate},
		{"revoked session", func() session.State { s := liveState(); s.Revoked = true; return s }(), "token-1", "jkt-lan", start, session.Revoked},
		{"idle expired", liveState(), "token-1", "jkt-lan", start.Add(31 * 24 * time.Hour), session.Expired},
		{"absolute expiry", func() session.State { s := liveState(); s.IdleExpiresAt = start.Add(100 * 24 * time.Hour); return s }(), "token-1", "jkt-lan", start.Add(91 * 24 * time.Hour), session.Expired},
		{"other device key", liveState(), "token-1", "jkt-thief", start, session.KeyMismatch},
		{"previous token inside grace", rotated, "token-1", "jkt-lan", start.Add(grace), session.RotateAgain},
		{"previous token after grace", rotated, "token-1", "jkt-lan", start.Add(grace + time.Nanosecond), session.Reuse},
		{"garbage secret with a real sid", rotated, "token-garbage", "jkt-lan", start, session.Unknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := session.Decide(c.state, hashOf(c.presented), c.jkt, c.now, grace); got != c.want {
				t.Fatalf("Decide = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRevokedWinsOverEveryOtherCheck(t *testing.T) {
	s := liveState()
	s.Revoked = true
	if got := session.Decide(s, hashOf("token-garbage"), "jkt-thief", start.Add(365*24*time.Hour), grace); got != session.Revoked {
		t.Fatalf("Decide = %v, want Revoked", got)
	}
}

func TestRetryingTheSameLostTokenTwiceRotatesBothTimes(t *testing.T) {
	s := session.Advance(liveState(), hashOf("token-2"), "jti-1", start, 30*24*time.Hour)
	at := start.Add(5 * time.Second)
	if got := session.Decide(s, hashOf("token-1"), "jkt-lan", at, grace); got != session.RotateAgain {
		t.Fatalf("first retry = %v, want RotateAgain", got)
	}
	s = session.Advance(s, hashOf("token-3"), "jti-2", at, 30*24*time.Hour)
	at = start.Add(10 * time.Second)
	if got := session.Decide(s, hashOf("token-1"), "jkt-lan", at, grace); got != session.RotateAgain {
		t.Fatalf("second retry = %v, want RotateAgain", got)
	}
}

func TestAdvanceKeepsTheNewestHistoryAndTrims(t *testing.T) {
	s := liveState()
	at := start
	for i := 2; i <= session.HistoryLimit+3; i++ {
		at = at.Add(time.Minute)
		s = session.Advance(s, hashOf(fmt.Sprintf("token-%d", i)), fmt.Sprintf("jti-%d", i), at, 30*24*time.Hour)
	}
	if len(s.History) != session.HistoryLimit {
		t.Fatalf("history = %d entries, want %d", len(s.History), session.HistoryLimit)
	}
	newestOld := fmt.Sprintf("token-%d", session.HistoryLimit+2)
	if string(s.History[0].Hash) != string(hashOf(newestOld)) {
		t.Fatal("history[0] is not the most recently replaced token")
	}
	late := at.Add(time.Hour)
	if got := session.Decide(s, hashOf("token-1"), "jkt-lan", late, grace); got != session.Unknown {
		t.Fatalf("trimmed token = %v, want Unknown", got)
	}
	if got := session.Decide(s, hashOf("token-5"), "jkt-lan", late, grace); got != session.Reuse {
		t.Fatalf("kept old token = %v, want Reuse", got)
	}
}

func TestAdvanceSlidesIdleExpiryButNeverPastTheAbsoluteLimit(t *testing.T) {
	s := liveState()
	s.ExpiresAt = start.Add(10 * 24 * time.Hour)
	next := session.Advance(s, hashOf("token-2"), "jti-1", start.Add(time.Hour), 30*24*time.Hour)
	if !next.IdleExpiresAt.Equal(s.ExpiresAt) {
		t.Fatalf("idle expiry = %v, want capped at %v", next.IdleExpiresAt, s.ExpiresAt)
	}
	s.ExpiresAt = start.Add(90 * 24 * time.Hour)
	next = session.Advance(s, hashOf("token-2"), "jti-1", start.Add(time.Hour), 30*24*time.Hour)
	if want := start.Add(time.Hour + 30*24*time.Hour); !next.IdleExpiresAt.Equal(want) {
		t.Fatalf("idle expiry = %v, want %v", next.IdleExpiresAt, want)
	}
}

func TestRecentJTIsKeepTheLastTwo(t *testing.T) {
	s := liveState()
	for i := 1; i <= 3; i++ {
		s = session.Advance(s, hashOf(fmt.Sprintf("token-%d", i+1)), fmt.Sprintf("jti-%d", i), start, 30*24*time.Hour)
	}
	if session.SeenJTI(s, "jti-1") {
		t.Fatal("jti-1 still remembered after two newer proofs")
	}
	if !session.SeenJTI(s, "jti-2") || !session.SeenJTI(s, "jti-3") {
		t.Fatal("the two latest jtis are not remembered")
	}
}

func TestAdvanceDoesNotShareHistoryWithThePreviousState(t *testing.T) {
	s := session.Advance(liveState(), hashOf("token-2"), "jti-1", start, time.Hour)
	before := len(s.History)
	_ = session.Advance(s, hashOf("token-3"), "jti-2", start, time.Hour)
	if len(s.History) != before || string(s.Current) != string(hashOf("token-2")) {
		t.Fatal("Advance mutated its input state")
	}
}

func TestMatchesAcceptsCurrentAndKeptHistoryOnly(t *testing.T) {
	s := session.Advance(liveState(), hashOf("token-2"), "jti-1", start, time.Hour)
	if !session.Matches(s, hashOf("token-2")) || !session.Matches(s, hashOf("token-1")) {
		t.Fatal("Matches rejected a real current or former token")
	}
	if session.Matches(s, hashOf("token-garbage")) {
		t.Fatal("Matches accepted a token the session never issued")
	}
}

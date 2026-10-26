package session

import (
	"crypto/subtle"
	"time"
)

const (
	HistoryLimit   = 20
	RecentJTILimit = 2
)

type Outcome int

const (
	Rotate Outcome = iota + 1
	RotateAgain
	Reuse
	Unknown
	KeyMismatch
	Expired
	Revoked
)

func (o Outcome) String() string {
	switch o {
	case Rotate:
		return "rotate"
	case RotateAgain:
		return "rotate_again"
	case Reuse:
		return "reuse"
	case Unknown:
		return "unknown"
	case KeyMismatch:
		return "key_mismatch"
	case Expired:
		return "expired"
	case Revoked:
		return "revoked"
	default:
		return "invalid"
	}
}

type HistoryEntry struct {
	Hash       []byte
	ReplacedAt time.Time
}

type State struct {
	Revoked       bool
	IdleExpiresAt time.Time
	ExpiresAt     time.Time
	KeyThumbprint string
	Current       []byte
	History       []HistoryEntry
	RecentJTI     []string
}

func Decide(s State, presented []byte, thumbprint string, now time.Time, grace time.Duration) Outcome {
	switch {
	case s.Revoked:
		return Revoked
	case !now.Before(s.IdleExpiresAt) || !now.Before(s.ExpiresAt):
		return Expired
	case !equal([]byte(thumbprint), []byte(s.KeyThumbprint)):
		return KeyMismatch
	case equal(presented, s.Current):
		return Rotate
	}
	for _, h := range s.History {
		if equal(presented, h.Hash) {
			if !now.After(h.ReplacedAt.Add(grace)) {
				return RotateAgain
			}
			return Reuse
		}
	}
	return Unknown
}

func Matches(s State, presented []byte) bool {
	found := equal(presented, s.Current)
	for _, h := range s.History {
		if equal(presented, h.Hash) {
			found = true
		}
	}
	return found
}

func SeenJTI(s State, jti string) bool {
	seen := false
	for _, j := range s.RecentJTI {
		if equal([]byte(j), []byte(jti)) {
			seen = true
		}
	}
	return seen
}

func Advance(s State, next []byte, jti string, now time.Time, idle time.Duration) State {
	history := make([]HistoryEntry, 0, HistoryLimit)
	history = append(history, HistoryEntry{Hash: s.Current, ReplacedAt: now})
	history = append(history, s.History...)
	if len(history) > HistoryLimit {
		history = history[:HistoryLimit]
	}
	recent := make([]string, 0, RecentJTILimit)
	recent = append(recent, jti)
	recent = append(recent, s.RecentJTI...)
	if len(recent) > RecentJTILimit {
		recent = recent[:RecentJTILimit]
	}
	idleUntil := now.Add(idle)
	if s.ExpiresAt.Before(idleUntil) {
		idleUntil = s.ExpiresAt
	}
	return State{
		Revoked:       s.Revoked,
		IdleExpiresAt: idleUntil,
		ExpiresAt:     s.ExpiresAt,
		KeyThumbprint: s.KeyThumbprint,
		Current:       next,
		History:       history,
		RecentJTI:     recent,
	}
}

func equal(a, b []byte) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare(a, b) == 1
}

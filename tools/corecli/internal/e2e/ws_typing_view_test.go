package e2e_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestTypingViewEndsOnlyAShownTypist(t *testing.T) {
	var v e2e.TypingView
	now := time.Unix(100, 0)
	a := e2e.Typist{Room: "7", User: "a"}
	if v.End(a) {
		t.Fatal("ended a typist never shown")
	}
	v.Start(a, now)
	if !v.End(a) || v.End(a) {
		t.Fatal("a shown typist did not end exactly once")
	}
	if _, ok := v.Next(); ok {
		t.Fatal("an empty view has a next expiry")
	}
}

func TestTypingViewExpiresAfterSixSecondsOfSilence(t *testing.T) {
	var v e2e.TypingView
	now := time.Unix(100, 0)
	a, b := e2e.Typist{Room: "7", User: "a"}, e2e.Typist{Room: "7", User: "b"}
	v.Start(b, now)
	v.Start(a, now.Add(time.Second))
	if next, ok := v.Next(); !ok || !next.Equal(now.Add(e2e.TypingTimeout)) {
		t.Fatalf("next = %v, %v, want b's deadline", next, ok)
	}
	if ended := v.Expire(now.Add(e2e.TypingTimeout - time.Millisecond)); len(ended) != 0 {
		t.Fatalf("expired %v before six seconds", ended)
	}
	v.Start(b, now.Add(3*time.Second))
	if ended := v.Expire(now.Add(8 * time.Second)); !slices.Equal(ended, []e2e.Typist{a}) {
		t.Fatalf("expired %v, want only a (b typed again)", ended)
	}
	if ended := v.Expire(now.Add(9 * time.Second)); !slices.Equal(ended, []e2e.Typist{b}) {
		t.Fatalf("expired %v, want b", ended)
	}
}

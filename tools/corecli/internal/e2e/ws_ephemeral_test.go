package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func presence(user string, online bool) *chatimv1.PresenceEntry {
	return &chatimv1.PresenceEntry{UserId: user, Online: online}
}

func TestWSFeedRecordsTypingAndItsEnd(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Typing("7", "a", true)
	f.Typing("7", "a", false)
	if i, _, ok := f.Find(0, e2e.TypingMatch("7", "a", true)); !ok || i != 0 {
		t.Fatalf("typing entry at %d, %v, want 0", i, ok)
	}
	if i, _, ok := f.Find(0, e2e.TypingMatch("7", "a", false)); !ok || i != 1 {
		t.Fatalf("stopped entry at %d, %v, want 1", i, ok)
	}
	if _, _, ok := f.Find(0, e2e.TypingMatch("8", "a", true)); ok {
		t.Fatal("a typing entry of room 7 matched room 8")
	}
	if n := f.Count(e2e.WSMatch{Frame: e2e.FrameTyping}); n != 2 {
		t.Fatalf("typing frames = %d, want 2", n)
	}
}

func TestWSFeedRecordsEachPresenceEntry(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Presence([]*chatimv1.PresenceEntry{presence("a", true), presence("d", false)})
	if f.Len() != 2 {
		t.Fatalf("entries = %d, want 2", f.Len())
	}
	if i, _, ok := f.Find(0, e2e.PresenceMatch("a", true)); !ok || i != 0 {
		t.Fatalf("a online at %d, %v, want 0", i, ok)
	}
	if i, _, ok := f.Find(0, e2e.PresenceMatch("d", false)); !ok || i != 1 {
		t.Fatalf("d offline at %d, %v, want 1", i, ok)
	}
	if _, _, ok := f.Find(0, e2e.PresenceMatch("a", false)); ok {
		t.Fatal("a online matched a offline")
	}
	if _, _, ok := f.Find(0, e2e.WSMatch{Frame: e2e.FrameTyping}); ok {
		t.Fatal("a presence entry matched a typing frame")
	}
}

func TestStayedOnlineCountsTheAnswersOfOneUser(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Presence([]*chatimv1.PresenceEntry{presence("a", true), presence("d", false)})
	f.Typing("7", "a", true)
	f.Presence([]*chatimv1.PresenceEntry{presence("a", true), presence("d", false)})
	n, err := e2e.StayedOnline(f.Entries(0), "a")
	if err != nil || n != 2 {
		t.Fatalf("StayedOnline = %d, %v, want 2 answers", n, err)
	}
}

func TestStayedOnlineRefusesAnOfflineAnswer(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Presence([]*chatimv1.PresenceEntry{presence("a", true)})
	f.Presence([]*chatimv1.PresenceEntry{presence("a", false)})
	if _, err := e2e.StayedOnline(f.Entries(0), "a"); err == nil {
		t.Fatal("StayedOnline accepted an offline answer")
	}
}

func TestStayedOnlineRefusesNoAnswer(t *testing.T) {
	f := e2e.NewWSFeed()
	f.Presence([]*chatimv1.PresenceEntry{presence("d", false)})
	if _, err := e2e.StayedOnline(f.Entries(0), "a"); err == nil {
		t.Fatal("StayedOnline accepted a window without any answer about a")
	}
}

package domain_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestEditKindsAreTheStoredValues(t *testing.T) {
	if domain.EditText != 1 || domain.EditDelete != 2 {
		t.Fatalf("EditText = %d, EditDelete = %d; want 1 and 2, the values stored in message_edits.k", domain.EditText, domain.EditDelete)
	}
}

func TestNewMessagesAndMembersCarryNoEditOrClearState(t *testing.T) {
	m := domain.Message{Room: 1, Seq: 1, Text: "hi", CreatedAt: time.UnixMilli(1)}
	if m.Version != 0 || m.Deleted || !m.EditedAt.IsZero() || m.Hidden {
		t.Fatalf("new message %+v carries edit state, want version 0 and no flags", m)
	}
	if (domain.Member{Room: 1, User: "alice"}).ClearedBeforeSeq != 0 {
		t.Fatal("a new member starts with cleared history")
	}
}

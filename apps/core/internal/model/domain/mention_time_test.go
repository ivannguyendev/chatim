package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

var (
	sentAt   = time.UnixMilli(1_700_000_000_000).UTC()
	editedAt = sentAt.Add(time.Minute)
)

func at(t domain.MentionTarget, when time.Time) domain.MentionTarget {
	t.At = when
	return t
}

func TestStampOnSendGivesEveryTargetTheSendTime(t *testing.T) {
	sent := domain.Mentions{Targets: []domain.MentionTarget{user("minh"), group("ops")}, All: true}.Stamp(domain.Mentions{}, sentAt)
	want := []domain.MentionTarget{at(user("minh"), sentAt), at(group("ops"), sentAt)}
	if !slices.Equal(sent.Targets, want) || !sent.All || !sent.AllAt.Equal(sentAt) {
		t.Fatalf("stamped = %+v, want %v and @all at %v", sent, want, sentAt)
	}
	if none := (domain.Mentions{}).Stamp(domain.Mentions{}, sentAt); none.Targets != nil || none.All || !none.AllAt.IsZero() {
		t.Fatalf("stamped empty set = %+v, want nothing", none)
	}
}

func TestStampOnEditKeepsTheTimeOfTargetsAlreadyThere(t *testing.T) {
	prev := domain.Mentions{Targets: []domain.MentionTarget{at(user("minh"), sentAt), at(user("lan"), sentAt)}, All: true, AllAt: sentAt}
	next := domain.Mentions{Targets: []domain.MentionTarget{group("ops"), user("minh")}, All: true}.Stamp(prev, editedAt)
	want := []domain.MentionTarget{at(group("ops"), editedAt), at(user("minh"), sentAt)}
	if !slices.Equal(next.Targets, want) || !next.AllAt.Equal(sentAt) {
		t.Fatalf("stamped = %+v, want %v and @all kept at %v", next, want, sentAt)
	}
	readded := domain.Mentions{All: true}.Stamp(domain.Mentions{}, editedAt)
	if !readded.AllAt.Equal(editedAt) {
		t.Fatalf("@all added by an edit = %v, want %v", readded.AllAt, editedAt)
	}
	dropped := domain.Mentions{}.Stamp(prev, editedAt)
	if dropped.All || !dropped.AllAt.IsZero() {
		t.Fatalf("@all dropped by an edit = %+v, want no time", dropped)
	}
}

func TestSameMentionsAndDedupeIgnoreTimes(t *testing.T) {
	if !domain.SameMentions([]domain.MentionTarget{at(user("minh"), sentAt)}, []domain.MentionTarget{user("minh")}) {
		t.Fatal("SameMentions compared target times")
	}
	got, err := domain.ValidateMentions([]domain.MentionTarget{at(user("minh"), sentAt), user("minh")}, domain.DefaultMentionTargetsMax)
	if err != nil || len(got) != 1 {
		t.Fatalf("ValidateMentions = %v, %v; want one target", got, err)
	}
}

func TestMessageMentionSetRoundTrips(t *testing.T) {
	set := domain.Mentions{Targets: []domain.MentionTarget{at(user("minh"), sentAt)}, All: true, AllAt: sentAt}
	m := domain.Message{}.WithMentions(set)
	if got := m.MentionSet(); !slices.Equal(got.Targets, set.Targets) || !got.All || !got.AllAt.Equal(sentAt) {
		t.Fatalf("MentionSet = %+v, want %+v", got, set)
	}
	if gone := (domain.Message{Deleted: true}).WithMentions(set).WithoutDeletedContent(); gone.Mentions != nil || gone.MentionAll || !gone.MentionAllAt.IsZero() {
		t.Fatalf("deleted message keeps mentions %+v", gone)
	}
}

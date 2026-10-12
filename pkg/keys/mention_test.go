package keys

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMentionKeyIsTheMessageKeyThenTheTargetKindThenTheID(t *testing.T) {
	msg := Msg(777, 0, 57)
	b := Mention(msg, MentionGroupKind, "team-design")
	want := append(append(append([]byte{}, msg...), 2), "team-design"...)
	if !bytes.Equal(b, want) {
		t.Fatalf("Mention = %x, want %x", b, want)
	}
	if MentionUserKind != 1 || MentionGroupKind != 2 || MentionAllKind != 3 {
		t.Fatalf("mention kinds = %d %d %d, want 1 2 3", MentionUserKind, MentionGroupKind, MentionAllKind)
	}
	if all := Mention(msg, MentionAllKind, ""); len(all) != MentionHeadLen || MentionHeadLen != MsgLen+1 {
		t.Fatalf("Mention(all) = %d bytes, want %d", len(all), MsgLen+1)
	}
}

func TestParseMentionRoundTrips(t *testing.T) {
	msg := Msg(777, 0, 57)
	for _, c := range []struct {
		kind MentionKind
		id   string
	}{
		{MentionUserKind, "minh"},
		{MentionGroupKind, strings.Repeat("g", MaxMentionID)},
		{MentionAllKind, ""},
	} {
		gotMsg, kind, id, err := ParseMention(Mention(msg, c.kind, c.id))
		if err != nil || !bytes.Equal(gotMsg, msg) || kind != c.kind || id != c.id {
			t.Fatalf("ParseMention(Mention(%d, %q)) = %x %d %q %v", c.kind, c.id, gotMsg, kind, id, err)
		}
	}
}

func TestMentionKeysOfAMessageStayInsideItsRange(t *testing.T) {
	lo, hi := Msg(777, 0, 57), Msg(777, 0, 58)
	for _, k := range [][]byte{
		Mention(lo, MentionUserKind, "a"),
		Mention(lo, MentionAllKind, ""),
		Mention(lo, MentionGroupKind, strings.Repeat("Z", MaxMentionID)),
	} {
		if bytes.Compare(k, lo) <= 0 || bytes.Compare(k, hi) >= 0 {
			t.Fatalf("mention key %x is outside (%x, %x)", k, lo, hi)
		}
	}
}

func TestParseMentionRejectsBadKeys(t *testing.T) {
	msg := Msg(777, 0, 57)
	lengths := [][]byte{
		nil,
		msg,
		Mention(msg, MentionUserKind, ""),
		Mention(msg, MentionGroupKind, strings.Repeat("g", MaxMentionID+1)),
		Mention(msg, MentionAllKind, "777"),
	}
	for _, b := range lengths {
		if _, _, _, err := ParseMention(b); !errors.Is(err, ErrLength) {
			t.Errorf("ParseMention(%d bytes) err = %v, want ErrLength", len(b), err)
		}
	}
	for _, kind := range []MentionKind{0, 4} {
		if _, _, _, err := ParseMention(Mention(msg, kind, "a")); !errors.Is(err, ErrKind) {
			t.Errorf("ParseMention(kind %d) err = %v, want ErrKind", kind, err)
		}
	}
}

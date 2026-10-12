package keys

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestInteractionKeyIsTheMessageKeyThenTheKindThenThePart(t *testing.T) {
	msg := Msg(9, 0, 40)
	b := Interaction(msg, BookmarkKind, []byte("minh"))
	want := append(append(append([]byte{}, msg...), 2), "minh"...)
	if !bytes.Equal(b, want) {
		t.Fatalf("Interaction = %x, want %x", b, want)
	}
	if ReactionKind != 1 || BookmarkKind != 2 || ReplyKind != 3 {
		t.Fatalf("kinds = %d %d %d, want 1 2 3", ReactionKind, BookmarkKind, ReplyKind)
	}
	if !bytes.Equal(InteractionUser(msg, ReactionKind, "hung"), Interaction(msg, ReactionKind, []byte("hung"))) {
		t.Fatal("InteractionUser must equal Interaction with the user bytes")
	}
}

func TestInteractionReplyKeysOfAMessageHaveOneLengthAndSortBySeq(t *testing.T) {
	msg := Msg(9, 0, 40)
	seqs := []uint64{0, 1, 41, 57, 256, math.MaxUint64 - 1}
	prev := []byte(nil)
	for _, seq := range seqs {
		k := InteractionReply(msg, 0, seq)
		if len(k) != InteractionReplyLen || InteractionReplyLen != 41 {
			t.Fatalf("InteractionReply(seq %d) has %d bytes, want 41", seq, len(k))
		}
		if !bytes.HasPrefix(k, InteractionPrefix(msg, ReplyKind)) {
			t.Fatalf("InteractionReply(seq %d) = %x lacks the reply prefix", seq, k)
		}
		if prev != nil && bytes.Compare(prev, k) >= 0 {
			t.Fatalf("reply keys out of seq order at seq %d", seq)
		}
		prev = k
	}
}

func TestInteractionReplyRangeHoldsOnlyTheRepliesOfOneMessage(t *testing.T) {
	msg := Msg(9, 0, 40)
	lo, hi := InteractionReplyRange(msg)
	if len(lo) != InteractionReplyLen || len(hi) != InteractionReplyLen {
		t.Fatalf("range bounds are %d and %d bytes, want both %d", len(lo), len(hi), InteractionReplyLen)
	}
	inside := func(k []byte) bool { return bytes.Compare(k, lo) >= 0 && bytes.Compare(k, hi) < 0 }
	for _, seq := range []uint64{0, 41, math.MaxUint64} {
		if k := InteractionReply(msg, 0, seq); !inside(k) {
			t.Fatalf("reply seq %d = %x is outside [%x, %x)", seq, k, lo, hi)
		}
	}
	outside := [][]byte{
		InteractionReply(Msg(9, 0, 39), 0, math.MaxUint64),
		InteractionReply(Msg(9, 0, 41), 0, 0),
		InteractionUser(msg, ReactionKind, strings.Repeat("z", 16)),
		InteractionUser(msg, BookmarkKind, strings.Repeat("z", 16)),
	}
	for _, k := range outside {
		if inside(k) {
			t.Fatalf("key %x is inside the reply range of another message or kind", k)
		}
	}
}

func TestParseInteractionReturnsTheKindAndThePart(t *testing.T) {
	msg := Msg(9, 3, 40)
	for _, c := range []struct {
		key  []byte
		kind InteractionKind
		part []byte
	}{
		{InteractionUser(msg, ReactionKind, "a"), ReactionKind, []byte("a")},
		{InteractionUser(msg, BookmarkKind, strings.Repeat("Z", MaxInteractionUser)), BookmarkKind, []byte(strings.Repeat("Z", MaxInteractionUser))},
		{InteractionReply(msg, 0, 57), ReplyKind, InteractionReply(msg, 0, 57)[InteractionHeadLen:]},
	} {
		gotMsg, kind, part, err := ParseInteraction(c.key)
		if err != nil || !bytes.Equal(gotMsg, msg) || kind != c.kind || !bytes.Equal(part, c.part) {
			t.Fatalf("ParseInteraction(%x) = %x %d %x %v", c.key, gotMsg, kind, part, err)
		}
	}
	_, _, part, _ := ParseInteraction(InteractionReply(msg, 7, 57))
	thread, seq, err := ParseReplyPart(part)
	if err != nil || thread != 7 || seq != 57 {
		t.Fatalf("ParseReplyPart = %d %d %v, want 7 57", thread, seq, err)
	}
}

func TestParseInteractionKeepsTheMessageKeyFromBeingOverwritten(t *testing.T) {
	key := InteractionUser(Msg(9, 0, 40), ReactionKind, "hung")
	msg, _, _, _ := ParseInteraction(key)
	_ = append(msg, 0xff)
	if key[MsgLen] != byte(ReactionKind) {
		t.Fatal("appending to the parsed message key overwrote the source key")
	}
}

func TestParseInteractionRejectsBadKeys(t *testing.T) {
	msg := Msg(9, 0, 40)
	lengths := [][]byte{
		nil,
		msg,
		Interaction(msg, ReactionKind, nil),
		Interaction(msg, BookmarkKind, make([]byte, MaxInteractionUser+1)),
		Interaction(msg, ReplyKind, make([]byte, 15)),
		Interaction(msg, ReplyKind, make([]byte, 17)),
	}
	for _, b := range lengths {
		if _, _, _, err := ParseInteraction(b); !errors.Is(err, ErrLength) {
			t.Errorf("ParseInteraction(%d bytes) err = %v, want ErrLength", len(b), err)
		}
	}
	for _, kind := range []InteractionKind{0, 4, 255} {
		if _, _, _, err := ParseInteraction(Interaction(msg, kind, []byte("a"))); !errors.Is(err, ErrKind) {
			t.Errorf("ParseInteraction(kind %d) err = %v, want ErrKind", kind, err)
		}
	}
	if _, _, err := ParseReplyPart(make([]byte, 15)); !errors.Is(err, ErrLength) {
		t.Errorf("ParseReplyPart(15 bytes) err = %v, want ErrLength", err)
	}
}

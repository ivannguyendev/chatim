package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestInteractionIDMatchesTheStoredKey(t *testing.T) {
	key := store.MsgKey{Room: 7, Thread: 0, Seq: 3}
	reaction := store.Interaction{Kind: keys.ReactionKind, Key: key, User: "bob"}
	if got, want := store.InteractionID(reaction), keys.InteractionUser(keys.Msg(7, 0, 3), keys.ReactionKind, "bob"); string(got) != string(want) {
		t.Fatalf("reaction id = %x, want %x", got, want)
	}
	reply := store.Interaction{Kind: keys.ReplyKind, Key: key, User: "bob", Reply: store.MsgKey{Room: 7, Seq: 9}}
	if got, want := store.InteractionID(reply), keys.InteractionReply(keys.Msg(7, 0, 3), 0, 9); string(got) != string(want) {
		t.Fatalf("reply id = %x, want %x", got, want)
	}
}

func TestCompareInteractionsOrdersByTimeThenShorterThenLowerID(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	of := func(seq uint64, user string, after time.Duration) store.Interaction {
		return store.Interaction{Kind: keys.ReactionKind, Key: store.MsgKey{Room: 7, Seq: seq}, User: user, At: at.Add(after)}
	}
	ordered := []store.Interaction{of(9, "zz", 0), of(1, "eve", 0), of(2, "eve", 0), of(1, "carol", 0), of(1, "a", time.Millisecond)}
	for i := range ordered {
		for j := range ordered {
			got, want := store.CompareInteractions(ordered[i], ordered[j]), compareInts(i, j)
			if got != want {
				t.Errorf("CompareInteractions(%d, %d) = %d, want %d", i, j, got, want)
			}
		}
	}
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func TestValidateInteractionScan(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	good := store.InteractionScan{Room: 7, Kind: keys.ReactionKind, From: from, To: from.Add(time.Hour), Limit: 10}
	if err := store.ValidateInteractionScan(good); err != nil {
		t.Fatalf("good scan: %v", err)
	}
	after := store.Interaction{Kind: keys.ReactionKind, Key: store.MsgKey{Room: 7, Seq: 1}, User: "bob", At: from}
	withCursor := good
	withCursor.After = &after
	if err := store.ValidateInteractionScan(withCursor); err != nil {
		t.Fatalf("scan with cursor: %v", err)
	}
	for name, mutate := range map[string]func(*store.InteractionScan){
		"bad kind":          func(q *store.InteractionScan) { q.Kind = keys.ReplyKind + 1 },
		"zero limit":        func(q *store.InteractionScan) { q.Limit = 0 },
		"limit over a page": func(q *store.InteractionScan) { q.Limit = store.MaxInteractionScan + 1 },
		"cursor of another kind": func(q *store.InteractionScan) {
			other := after
			other.Kind = keys.BookmarkKind
			q.After = &other
		},
		"cursor of another room": func(q *store.InteractionScan) {
			other := after
			other.Key.Room = 8
			q.After = &other
		},
	} {
		q := good
		mutate(&q)
		if err := store.ValidateInteractionScan(q); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateInteractionScan = %v, want ErrInvalidArgument", name, err)
		}
	}
}

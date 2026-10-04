package memstore_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestFeedLosesHistoryUntilForgotten(t *testing.T) {
	feed := memstore.NewFeed(memstore.NewMessages())
	feed.LoseHistory()
	if _, err := feed.Open(t.Context()); !errors.Is(err, store.ErrFeedHistoryLost) {
		t.Fatalf("Open after lost history = %v, want ErrFeedHistoryLost", err)
	}
	if err := feed.Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, err := feed.Open(t.Context()); err != nil {
		t.Fatalf("Open after Forget: %v", err)
	}
}

func TestFeedRejectsForeignPositions(t *testing.T) {
	cur, err := memstore.NewFeed(memstore.NewMessages()).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, pos := range []store.Position{nil, store.Position("x"), store.Position("-1")} {
		if err := cur.Confirm(t.Context(), pos); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("Confirm(%q) = %v, want ErrInvalidArgument", pos, err)
		}
	}
}

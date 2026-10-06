package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func RunEditFeed(t *testing.T, open func(t *testing.T) (store.Edits, store.ChangeFeed)) {
	t.Helper()
	t.Run("edit inserts come out in commit order with their content", func(t *testing.T) {
		edits, feed := open(t)
		cur := openCursor(t, feed)
		want := []domain.Edit{fact(roomA, mainThread, 1, 1), fact(roomB, sideThread, 2, 1), deletion(roomA, mainThread, 1, 2)}
		mustAppend(t, edits, want...)
		got := nextChanges(t, cur, len(want))
		facts := make([]domain.Edit, len(got))
		for i, c := range got {
			if c.Kind != store.EditInserted || c.Msg.Seq != 0 || c.Room != (domain.Room{}) {
				t.Fatalf("change %d = %+v, want only an edit", i, c)
			}
			facts[i] = c.Edit
		}
		assertEdits(t, "edit changes", facts, want)
	})
}

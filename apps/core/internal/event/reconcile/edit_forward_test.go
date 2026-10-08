package reconcile_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestForwardsAnEditInsertAsAnEditRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		committed := time.Now()
		rg.edit(t, room, 1, 2)
		synctest.Wait()
		stored := rg.js.Stored()
		if len(stored) != 1 {
			t.Fatalf("stored = %v, want one edit record", storedIDs(rg.js))
		}
		if want := work.Subject(setup.SubjectRoot, work.Partition(room, setup.Partitions)); stored[0].Subject != want {
			t.Fatalf("subject = %q, want %q", stored[0].Subject, want)
		}
		got, err := work.Decode(stored[0].Data)
		if err != nil || got.Kind != store.EditInserted || got.Room != room || got.Thread != 0 || got.Seq != 1 || got.Version != 2 || !got.CommittedAt.Equal(committed) {
			t.Fatalf("record = %+v, %v; want edit %d/0/1 v2 committed at %v", got, err, room, committed)
		}
		if id := publishtest.MsgID(stored[0]); id != "e:4242-0-1-v2" {
			t.Fatalf("msg id = %q, want e:4242-0-1-v2", id)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0", got)
		}
	})
}

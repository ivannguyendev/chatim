package reconcile_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestForwardsAMessageInsertAtOnceAsARecordOnItsPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		committed := time.Now()
		rg.insert(t, room, 1)
		synctest.Wait()
		stored := rg.js.Stored()
		if len(stored) != 1 {
			t.Fatalf("stored = %v, want one record without any delay", storedIDs(rg.js))
		}
		if want := work.Subject(setup.SubjectRoot, work.Partition(room, setup.Partitions)); stored[0].Subject != want {
			t.Fatalf("subject = %q, want %q", stored[0].Subject, want)
		}
		got, err := work.Decode(stored[0].Data)
		if err != nil || got.Kind != store.MessageInserted || got.Room != room || got.Thread != 0 || got.Seq != 1 || !got.CommittedAt.Equal(committed) {
			t.Fatalf("record = %+v, %v; want message %d/0/1 committed at %v", got, err, room, committed)
		}
		if id := publishtest.MsgID(stored[0]); id != got.ID() || id != recordID(1) {
			t.Fatalf("msg id = %q, want %q", id, recordID(1))
		}
	})
}

func TestForwardsARoomInsertAndItsCreationMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.createRoom(t, otherRoom)
		synctest.Wait()
		stored := rg.js.Stored()
		member := work.Record{Kind: store.MemberChanged, Room: otherRoom, Version: 1, User: "alice"}.ID()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{roomRecordID(otherRoom), member}) {
			t.Fatalf("stored = %v, want %s then %s", got, roomRecordID(otherRoom), member)
		}
		want := work.Subject(setup.SubjectRoot, work.Partition(otherRoom, setup.Partitions))
		for _, m := range stored {
			if m.Subject != want {
				t.Fatalf("subject = %q, want %q", m.Subject, want)
			}
		}
		if got, err := work.Decode(stored[0].Data); err != nil || got.Kind != store.RoomInserted || got.Room != otherRoom {
			t.Fatalf("record = %+v, %v; want a room record for %d", got, err, otherRoom)
		}
		if got, err := work.Decode(stored[1].Data); err != nil || got.Kind != store.MemberChanged || got.User != "alice" || got.Version != 1 {
			t.Fatalf("record = %+v, %v; want alice's creation member at ver 1", got, err)
		}
	})
}

func TestForwardsAReadPositionChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		if _, _, err := rg.rooms.MarkRead(t.Context(), room, "alice", 7); err != nil {
			t.Fatalf("MarkRead: %v", err)
		}
		synctest.Wait()
		want := work.Record{Kind: store.ReadChanged, Room: room, Version: 1, User: "alice"}.ID()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{want}) {
			t.Fatalf("stored = %v, want only %s", got, want)
		}
		if got, err := work.Decode(rg.js.Stored()[0].Data); err != nil || got.Kind != store.ReadChanged || got.User != "alice" || got.Version != 1 {
			t.Fatalf("record = %+v, %v; want alice's read ver 1", got, err)
		}
	})
}

func TestConfirmWaitsForTheAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 0 {
			t.Fatalf("confirmed before the ack = %d, want 0", got)
		}
		rg.js.Release()
		time.Sleep(tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed after the ack = %d, want 1", got)
		}
	})
}

func TestRefusedPublishIsResent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		refused := 0
		rg.js.RefuseWhen(func(*nats.Msg) error {
			if refused == 0 {
				refused++
				return errors.New("too many stalled")
			}
			return nil
		})
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) < 2 {
			t.Fatalf("attempts = %v, want the refused publish resent", got)
		}
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1)) {
			t.Fatalf("stored = %v, want %s once", got, recordID(1))
		}
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed = %d, want 1", got)
		}
	})
}

func TestFullWindowWaitsForTheHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1, 2, 3, 4, 5, 6)
		time.Sleep(tick)
		synctest.Wait()
		if got := len(rg.js.Attempts()); got != setup.Window {
			t.Fatalf("attempts with a full window = %d, want %d", got, setup.Window)
		}
		rg.js.Release()
		time.Sleep(tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2, 3, 4, 5, 6)) {
			t.Fatalf("stored after release = %v, want seq 1..6 in order", got)
		}
	})
}

func TestPersistentRefusalIsLoggedAndRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.RefuseWhen(func(*nats.Msg) error { return errors.New("no responders") })
		rg.insert(t, room, 1)
		time.Sleep(4 * tick)
		synctest.Wait()
		attempts := attemptIDs(rg.js)
		if len(attempts) < 3 {
			t.Fatalf("attempts = %v, want the refused publish retried", attempts)
		}
		if got := rg.sink.Count(failedMsg); got < 1 || got > len(attempts) {
			t.Fatalf("%q logged %d times for %d attempts, want at least once and at most once per attempt", failedMsg, got, len(attempts))
		}
		if got := rg.confirmed(t); got != 0 || len(rg.js.Stored()) != 0 {
			t.Fatalf("confirmed = %d, stored = %v; want nothing past a refused publish", got, storedIDs(rg.js))
		}
	})
}

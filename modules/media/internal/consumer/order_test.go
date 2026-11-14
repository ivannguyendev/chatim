package consumer_test

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/consumer"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
)

const orderFile = "acme/an/20261108/0123456789abcdefghijklmnop"

func TestEventsOfOneMessageRunInDeliveryOrderOneAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		now := time.Now()
		s := newStream(delivery(t, created(1, now, 1), 1), delivery(t, deleted(1, now), 1), delivery(t, created(2, now, 1), 1))
		run := start(t.Context(), r.consumer(t, s, cfg()))
		synctest.Wait()
		if calls := r.sync.recorded(); r.sync.inFlight() != 2 || len(calls) != 2 {
			t.Fatalf("in flight %d calls %+v, want the delete of seq 1 to wait", r.sync.inFlight(), calls)
		}
		close(r.sync.block)
		synctest.Wait()
		run.stop(t)
		var seqOne []attach.Kind
		for _, c := range r.sync.recorded() {
			if c.ev.Ref.Seq == 1 {
				seqOne = append(seqOne, c.kind)
			}
		}
		if len(seqOne) != 2 || seqOne[0] != attach.Created || seqOne[1] != attach.Deleted {
			t.Fatalf("seq 1 ran %v, want created then deleted", seqOne)
		}
	})
}

type staleChat struct{}

func (staleChat) MessageFiles(context.Context, domain.MessageRef) (domain.MessageState, error) {
	return domain.MessageState{Found: true, Tenant: tenant, Keys: []string{orderFile}}, nil
}

func (staleChat) RoomAvatars(context.Context, string) (domain.RoomAvatarState, error) {
	return domain.RoomAvatarState{}, nil
}

type plainObjects struct{}

func (plainObjects) Copy(context.Context, string, int64, string) error { return nil }

func (plainObjects) Stat(context.Context, domain.Bucket, string) (domain.Object, bool, error) {
	return domain.Object{}, false, nil
}

func (plainObjects) DeleteProcessing(context.Context, string) error { return nil }

func TestACreateAndDeleteOfOneMessageInFlightTogetherLeaveNoLivePair(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		uploads, files := memstore.NewUploads(), memstore.NewMessageFiles()
		now := time.Now()
		if err := uploads.Insert(t.Context(), domain.Upload{Key: orderFile, Tenant: tenant, ContentType: "image/png", Size: 9, State: domain.StateUploaded, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		log := slog.New(slog.DiscardHandler)
		svc, err := attach.New(attach.Config{}, attach.Deps{Chat: staleChat{}, Uploads: uploads, Files: files, Avatars: memstore.NewRoomAvatars(), Objects: plainObjects{}, Log: log})
		if err != nil {
			t.Fatal(err)
		}
		c, err := consumer.New(cfg(), consumer.Deps{Source: newStream(delivery(t, created(1, now, 1), 1), delivery(t, deleted(1, now), 1)), Sync: svc, Stats: svc.Stats(), Ready: func() bool { return true }, Log: log})
		if err != nil {
			t.Fatal(err)
		}
		run := start(t.Context(), c)
		synctest.Wait()
		run.stop(t)
		live, err := files.Active(t.Context(), tenant, "777-0-1")
		if err != nil || len(live) != 0 {
			t.Fatalf("live pairs %v, %v", live, err)
		}
		if u, err := uploads.Get(t.Context(), orderFile); err != nil || u.RetiredAt.IsZero() {
			t.Fatalf("upload %+v, %v; want the delete to retire it after the create", u, err)
		}
	})
}

func TestRoomUpdatesOfOneRoomRunOneAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		now := time.Now()
		s := newStream(delivery(t, roomUpdate(now), 1), delivery(t, roomUpdate(now), 1), delivery(t, created(1, now, 1), 1))
		run := start(t.Context(), r.consumer(t, s, cfg()))
		synctest.Wait()
		if calls := r.sync.recorded(); r.sync.inFlight() != 2 || len(calls) != 2 {
			t.Fatalf("in flight %d calls %+v, want the second room update to wait", r.sync.inFlight(), calls)
		}
		close(r.sync.block)
		synctest.Wait()
		run.stop(t)
		rooms := 0
		for _, c := range r.sync.recorded() {
			if c.kind == attach.RoomUpdated {
				rooms++
			}
		}
		if rooms != 2 {
			t.Fatalf("room updates ran %d times, want 2", rooms)
		}
	})
}

package actor_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewRouterValidatesInputs(t *testing.T) {
	msgs, rooms, sub, cids := memstore.NewMessages(), memstore.NewRooms(), &fakeSubmitter{}, &fakeRegistry{}
	mutations := map[string]func(*actor.Config){
		"zero mailbox":        func(c *actor.Config) { c.Mailbox = 0 },
		"negative idle":       func(c *actor.Config) { c.Idle = -time.Second },
		"zero max group":      func(c *actor.Config) { c.MaxGroup = 0 },
		"zero max actors":     func(c *actor.Config) { c.MaxActors = 0 },
		"zero group deadline": func(c *actor.Config) { c.GroupDeadline = 0 },
		"zero reservation":    func(c *actor.Config) { c.ReservationTTL = 0 },
		"reservation too short": func(c *actor.Config) {
			c.ReservationTTL = c.GroupDeadline + time.Second
		},
	}
	for name, mutate := range mutations {
		cfg := baseConfig
		mutate(&cfg)
		if _, err := actor.NewRouter(msgs, rooms, sub, cids, cfg, quiet); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: NewRouter = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := actor.NewRouter(nil, rooms, sub, cids, baseConfig, quiet); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("NewRouter(nil messages) = %v, want ErrInvalidArgument", err)
	}
	if _, err := actor.NewRouter(msgs, rooms, nil, cids, baseConfig, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("NewRouter(nil submitter) = %v, want ErrInvalidArgument", err)
	}
	if _, err := actor.NewRouter(msgs, rooms, sub, nil, baseConfig, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("NewRouter(nil cid registry) = %v, want ErrInvalidArgument", err)
	}
}

func TestSendValidatesCommandBeforeRouting(t *testing.T) {
	rg := started(t, baseConfig)
	tests := map[string]struct {
		mutate func(*actor.SendCmd)
		field  string
	}{
		"thread reply":   {func(c *actor.SendCmd) { c.Thread = 7 }, "thread"},
		"missing cid":    {func(c *actor.SendCmd) { c.CID = "" }, "cid"},
		"cid with dot":   {func(c *actor.SendCmd) { c.CID = "c.1" }, "cid"},
		"blank text":     {func(c *actor.SendCmd) { c.Text = "  \t" }, "text"},
		"text too large": {func(c *actor.SendCmd) { c.Text = strings.Repeat("a", 16385) }, "text"},
		"bad tenant":     {func(c *actor.SendCmd) { c.Tenant = "Acme" }, "tenant"},
		"bad user":       {func(c *actor.SendCmd) { c.User = "a b" }, "user"},
		"zero room":      {func(c *actor.SendCmd) { c.Room = 0 }, "room"},
	}
	for name, tt := range tests {
		c := cmd(roomA, "alice", "c1")
		tt.mutate(&c)
		_, err := rg.Send(context.Background(), c)
		if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+tt.field) {
			t.Errorf("%s: Send = %v, want ErrInvalidArgument naming %q", name, err, tt.field)
		}
	}
	if n := rg.ActorCount(); n != 0 {
		t.Errorf("%d actors started for invalid commands, want 0", n)
	}
}

func TestSendRejectsWrongTenantAsMissingRoom(t *testing.T) {
	rg := started(t, baseConfig)
	c := cmd(roomA, "alice", "c1")
	c.Tenant = "other"
	_, err := rg.Send(context.Background(), c)
	expectErr(t, err, domain.ErrRoomNotFound)
	if len(rg.sub.sent()) != 0 {
		t.Fatal("message of a foreign tenant reached the flusher")
	}
}

func TestSendChecksMembershipAndCachesOnlyMembers(t *testing.T) {
	rg := started(t, baseConfig)
	for range 2 {
		_, err := rg.Send(context.Background(), cmd(roomA, "carol", "c1"))
		expectErr(t, err, domain.ErrNotMember)
	}
	if n := rg.rooms.memberCalls(); n != 2 {
		t.Fatalf("non-member looked up %d times, want 2 (never cached)", n)
	}
	mustSend(t, rg.Router, cmd(roomA, "alice", "a1"))
	mustSend(t, rg.Router, cmd(roomA, "alice", "a2"))
	if n := rg.rooms.memberCalls(); n != 3 {
		t.Fatalf("member lookups = %d, want 3 (alice cached after the first)", n)
	}
}

func TestMembershipStoreFailureIsRetryLater(t *testing.T) {
	rg := started(t, baseConfig)
	rg.rooms.memberErr = errors.New("members collection down")
	_, err := rg.Send(context.Background(), cmd(roomA, "alice", "c1"))
	expectErr(t, err, domain.ErrRetryLater)
}

func TestUnknownRoomFailsQueuedCommandsAndRemovesActor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		const ghost uint64 = 999
		var waits []<-chan sendResult
		for _, cid := range []string{"g1", "g2", "g3"} {
			waits = append(waits, sendAsync(t.Context(), rg.Router, cmd(ghost, "alice", cid)))
		}
		for _, w := range waits {
			expectErr(t, (<-w).err, domain.ErrRoomNotFound)
		}
		synctest.Wait()
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors left for a missing room, want 0", n)
		}
		createRoom(t, rg.rooms, ghost, "alice")
		if ack := mustSend(t, rg.Router, cmd(ghost, "alice", "g1")); ack.Seq != 1 {
			t.Fatalf("first message in a new room got seq %d, want 1", ack.Seq)
		}
	})
}

func TestRoomLoadFailureIsRetryLaterAndRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		rg.rooms.mu.Lock()
		rg.rooms.getErr = errors.New("rooms collection down")
		rg.rooms.mu.Unlock()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		synctest.Wait()
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("%d actors left after a failed load, want 0", n)
		}
		rg.rooms.mu.Lock()
		rg.rooms.getErr = nil
		rg.rooms.mu.Unlock()
		mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
	})
}

func TestAckCarriesStoredMillisecondUTCTimestamp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		time.Sleep(1234567 * time.Nanosecond)
		want := time.Now().UTC().Truncate(time.Millisecond)
		ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
		if ack.Seq != 1 || ack.Pts != 1 {
			t.Fatalf("ack = %+v, want seq 1 pts 1", ack)
		}
		if !ack.CreatedAt.Equal(want) || ack.CreatedAt.Location() != time.UTC {
			t.Fatalf("CreatedAt = %v, want %v (UTC, truncated to milliseconds)", ack.CreatedAt, want)
		}
		docs := storedCIDs(t, rg.msgs.Messages, roomA)["c1"]
		if len(docs) != 1 {
			t.Fatalf("stored %d copies of c1, want 1", len(docs))
		}
		doc := docs[0]
		assertAckMatches(t, ack, doc)
		if doc.Thread != 0 || doc.Kind != domain.KindText || doc.Tenant != tenant || doc.From != "alice" || doc.Text != "hello c1" {
			t.Fatalf("stored doc = %+v", doc)
		}
	})
}

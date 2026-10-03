package publish_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestEventsArePublishedInArrivalOrderWithoutWaitingForAcks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastSetup)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1, 2)
		rg.enqueue(t, roomA, 3)
		synctest.Wait()
		if n := rg.js.Held(); n != 3 {
			t.Fatalf("%d publishes in flight, want all 3 without waiting for acks", n)
		}
		rg.js.Release()
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"101-0-1", "101-0-2", "101-0-3"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want %v", got, want)
		}
	})
}

func TestARefusedPublishDropsOnlyThatEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := fastSetup
		cfg.Shards = 1
		rg := started(t, cfg)
		rg.js.RefuseWhen(func(m *nats.Msg) error {
			if publishtest.MsgID(m) == "101-0-1" {
				return errRefused
			}
			return nil
		})
		rg.enqueue(t, roomA, 1)
		rg.enqueue(t, roomA, 2)
		rg.enqueue(t, roomB, 1)
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"101-0-2", "202-0-1"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want %v", got, want)
		}
		if n := rg.sink.Count(refusedMsg); n != 1 {
			t.Fatalf("logged %d refused publishes, want 1", n)
		}
		if n := attemptsOf(rg.js, "101-0-1"); n != 1 {
			t.Fatalf("refused event attempted %d times, want 1", n)
		}
	})
}

func TestAsyncFailuresAreLoggedAtMostOncePerSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &testlog.Sink{}
		handle := publish.AsyncFailureHandler(sink.Logger())
		msg := &nats.Msg{Subject: "evt.acme.room.101.msg_created", Header: nats.Header{}}
		msg.Header.Set(jetstream.MsgIDHeader, "101-0-1")
		for range 5 {
			handle(nil, msg, errRefused)
		}
		if n := sink.Count(failedMsg); n != 1 {
			t.Fatalf("logged %d failures in one second, want 1", n)
		}
		time.Sleep(time.Second)
		handle(nil, msg, errRefused)
		if n := sink.Count(failedMsg); n != 2 {
			t.Fatalf("logged %d failures after a second, want 2", n)
		}
	})
}

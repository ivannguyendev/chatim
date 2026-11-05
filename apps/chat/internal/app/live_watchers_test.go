package app

import (
	"errors"
	"testing"
)

func TestLiveWatchersStopAtTheFirstFailedStart(t *testing.T) {
	member, room, later := &fakeWatch{}, &fakeWatch{startErr: errBroken}, &fakeWatch{}
	ws := liveWatchers{member, room, later}
	if err := ws.Start(t.Context()); !errors.Is(err, errBroken) {
		t.Fatalf("Start = %v, want the room watch error", err)
	}
	if !member.started.Load() || !room.started.Load() || later.started.Load() {
		t.Errorf("started member %v room %v later %v, want only the watches up to the failure",
			member.started.Load(), room.started.Load(), later.started.Load())
	}
	ws.Stop()
	for i, w := range ws {
		if !w.(*fakeWatch).stopped.Load() {
			t.Errorf("watch %d not stopped", i)
		}
	}
}

func TestServeNeverOpensGRPCWhenTheRoomWatchFailsToStart(t *testing.T) {
	running := make(chan struct{})
	close(running)
	member, room := &fakeWatch{}, &fakeWatch{startErr: errBroken}
	rg := startGate(t, t.Context(), &gatedRouter{running: running}, idle{}, liveWatchers{member, room})
	if err := rg.wait(t); !errors.Is(err, errBroken) {
		t.Fatalf("serve = %v, want the room watch error", err)
	}
	rg.assertGRPCNeverServed(t)
	if !member.stopped.Load() || !room.stopped.Load() {
		t.Errorf("member watch stopped %v, room watch stopped %v; want both", member.stopped.Load(), room.stopped.Load())
	}
}

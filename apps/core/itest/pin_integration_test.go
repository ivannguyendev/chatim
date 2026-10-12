package itest

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraWorkersProjectAPinFactWrittenOutsideTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	first := sendAs(t, client, itUser, roomID, "pin-b-1", "pinned outside the core")
	second := sendAs(t, client, itUser, roomID, "pin-b-2", "pinned through the core")
	core.awaitTerm(t)

	st := itStore(it, core)
	at := time.Now().UTC().Truncate(time.Millisecond)
	fact := domain.PinAction{Room: room, PV: 1, Tenant: itTenant, Op: domain.PinOpPin, Seq: first, By: itUser, At: at}
	if err := itPins(st).Append(t.Context(), fact); err != nil {
		t.Fatalf("append a pin outside the core: %v", err)
	}
	pinnedID := pbconv.PinEventID(room, 1)
	ev := awaitLiveEvents(t, live, pinnedID)[pinnedID]
	if p := ev.GetMessagePinned(); p.GetPinVer() != 1 || p.GetMessage().GetSeq() != first || p.GetMessage().GetText() != "pinned outside the core" || ev.GetActor() != itUser {
		t.Fatalf("msg_pinned from the workers = %v, want seq %d at pin version 1 by %s", ev, first, itUser)
	}
	state, err := st.PinState(t.Context(), room)
	if err != nil || state.Version != 1 || len(state.Pins) != 1 || state.Pins[0].Seq != first || state.Pins[0].PV != 1 || !state.Pins[0].At.Equal(at) {
		t.Fatalf("projection = %+v, %v; want seq %d pinned at version 1", state, err, first)
	}

	resp, err := client.PinMessage(caller(t.Context()), &chatimv1.PinMessageRequest{RoomId: roomID, Seq: second})
	if err != nil || resp.GetPinVer() != 2 || !slices.Equal(pinnedSeqs(resp.GetPins()), []uint64{second, first}) {
		t.Fatalf("PinMessage = %v, %v; want version 2 with the new pin first", resp, err)
	}
	unpin := &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: first}
	for attempt := range 2 {
		resp, err := client.UnpinMessage(caller(t.Context()), unpin)
		if err != nil || resp.GetPinVer() != 3 || !slices.Equal(pinnedSeqs(resp.GetPins()), []uint64{second}) {
			t.Fatalf("UnpinMessage attempt %d = %v, %v; want version 3 with only seq %d", attempt+1, resp, err, second)
		}
	}
	unpinnedID := pbconv.PinEventID(room, 3)
	if u := awaitLiveEvents(t, live, unpinnedID)[unpinnedID].GetMessageUnpinned(); u.GetPinVer() != 3 || u.GetMessage().GetSeq() != first {
		t.Fatalf("msg_unpinned = %v, want seq %d at pin version 3", u, first)
	}
}

func TestRealInfraPinLimitHoldsUnderConcurrentPins(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["PIN_LIMIT"] = "3"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	seqs := make([]uint64, 6)
	for i := range seqs {
		seqs[i] = sendAs(t, client, itUser, roomID, "pin-d-"+strconv.Itoa(i+1), "pin candidate")
	}

	errs := make([]error, len(seqs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, seq := range seqs {
		req := &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}
		wg.Go(func() {
			<-start
			_, errs[i] = retryingUnavailable(caller(t.Context()), func(ctx context.Context) (*chatimv1.PinMessageResponse, error) {
				return client.PinMessage(ctx, req)
			})
		})
	}
	close(start)
	wg.Wait()
	pinned := 0
	for i, err := range errs {
		switch status.Code(err) {
		case codes.OK:
			pinned++
		case codes.FailedPrecondition:
		default:
			t.Fatalf("pin of seq %d = %v, want success or FailedPrecondition", seqs[i], err)
		}
	}
	if pinned != 3 {
		t.Fatalf("%d of %d concurrent pins succeeded, want exactly PIN_LIMIT 3", pinned, len(seqs))
	}
	st := itStore(it, core)
	facts, err := itPins(st).After(t.Context(), room, 0, store.MaxPinScan)
	if err != nil || len(facts) != 3 {
		t.Fatalf("pin facts = %+v, %v; want exactly 3", facts, err)
	}
	read := func() (domain.PinState, error) { return st.PinState(t.Context(), room) }
	state := awaitStored(t, "pins of the room", read, func(s domain.PinState) bool { return s.Version >= 3 })
	if state.Version != 3 || len(state.Pins) != 3 {
		t.Fatalf("projection = %+v, want 3 pins at version 3", state)
	}
}

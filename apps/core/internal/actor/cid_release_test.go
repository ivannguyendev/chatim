package actor_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestOnlyDefiniteFailuresReleaseTheReservation(t *testing.T) {
	tests := map[string]struct {
		arrange func(*rig)
		release bool
	}{
		"rejected write":        {func(rg *rig) { rg.sub.then(rejected) }, true},
		"submit refused":        {func(rg *rig) { rg.sub.err = domain.ErrBusy }, true},
		"reassign limit":        {func(rg *rig) { rg.sub.alwaysDo(rg.sub.foreignFirst) }, true},
		"resend limit":          {func(rg *rig) { rg.sub.alwaysDo(lostUnknown) }, false},
		"rejected resend":       {func(rg *rig) { rg.sub.then(lostUnknown, rejected) }, false},
		"unconfirmed deadline":  {func(rg *rig) { rg.sub.then(rg.sub.landedUnknown); rg.msgs.findHook = findTimesOut }, false},
		"unconfirmed duplicate": {func(rg *rig) { rg.sub.then(rg.sub.landedDuplicate); rg.msgs.findHook = findTimesOut }, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rg := newRig(t, baseConfig)
				tt.arrange(rg)
				rg.start(t)
				if _, err := rg.Send(t.Context(), cmd(roomA, "alice", "x")); err == nil {
					t.Fatal("failing write was acked")
				}
				var want [][]dedupe.Key
				if tt.release {
					want = [][]dedupe.Key{{remoteKey(roomA, "alice", "x")}}
				}
				if _, commits, aborts := rg.cids.calls(); !slices.EqualFunc(aborts, want, slices.Equal) || len(commits) != 0 {
					t.Fatalf("aborts %v commits %v, want aborts %v and no commits", aborts, commits, want)
				}
			})
		})
	}
}

func TestHardStopKeepsInFlightReservations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.hold()
		rg.start(t)
		w := sendAsync(t.Context(), rg.Router, cmd(roomA, "alice", "x"))
		synctest.Wait()
		rg.cancel()
		expectErr(t, (<-w).err, domain.ErrRetryLater)
		if _, _, aborts := rg.cids.calls(); len(aborts) != 0 {
			t.Fatalf("hard stop released in-flight reservations: %v", aborts)
		}
	})
}

func findTimesOut(int) error { return errors.New("find timed out") }

package slot

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestHookTimeoutDefaultsToHalfATick(t *testing.T) {
	_, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a")
	if want := a.cfg.Tick / 2; a.cfg.HookTimeout != want {
		t.Fatalf("HookTimeout = %v, want %v", a.cfg.HookTimeout, want)
	}
}

func TestBeforeReleaseSeesSlotsUnownedButStillLeased(t *testing.T) {
	mr, rdb := newRedis(t)
	released := &hookLog{}
	a := newManager(t, rdb, "core-a", withBeforeRelease(released.record))
	claimLeftovers(t, mr, a, crowdedForRelease)
	released.during = func(_ context.Context, _ string, slots []uint16) {
		for _, s := range slots {
			if owner, _ := mr.Get(slotmap.SlotKey(s)); a.Owns(s) || owner != "core-a" {
				t.Errorf("slot %d during BeforeRelease: Owns %v, lease %q; want unowned but still leased", s, a.Owns(s), owner)
			}
		}
	}
	heartbeat(t, mr, "core-y")
	stepAll(t, a)
	batches := released.batches()
	if len(batches) != 1 || len(batches[0]) != 3 || !distinct(batches[0]) {
		t.Fatalf("BeforeRelease batches %v, want one batch of the 3 surplus slots", batches)
	}
	if !sameSlots(batches[0], without(slotRange(crowdedForRelease, slotmap.Count), a.Owned())) {
		t.Fatalf("BeforeRelease batch %v differs from the slots core-a gave up", batches[0])
	}
	assertLeasesGone(t, mr, batches[0])
}

func TestStepReleasingSlotsEndsWithinOneTickWhenBeforeReleaseBlocks(t *testing.T) {
	mr, rdb := newRedis(t)
	released := &hookLog{block: true}
	a := newManager(t, rdb, "core-a", withBeforeRelease(released.record))
	claimLeftovers(t, mr, a, crowdedForRelease)
	heartbeat(t, mr, "core-y")
	begin := time.Now()
	stepAll(t, a)
	if took := time.Since(begin); took >= a.cfg.Tick {
		t.Fatalf("Step took %v with a blocking BeforeRelease, want under one tick %v", took, a.cfg.Tick)
	}
	calls := released.all()
	if len(calls) != 1 || len(calls[0].slots) != 3 {
		t.Fatalf("BeforeRelease batches %v, want one batch of the 3 surplus slots", released.batches())
	}
	if c := calls[0]; !errors.Is(c.ctxErr, context.DeadlineExceeded) || !nearStepStart(begin, c.deadline, a.cfg.HookTimeout) {
		t.Fatalf("blocking hook ended with %v at deadline +%v, want DeadlineExceeded within HookTimeout %v", c.ctxErr, c.deadline.Sub(begin), a.cfg.HookTimeout)
	}
	assertLeasesGone(t, mr, calls[0].slots)
	if n := len(a.Owned()); n != 342 {
		t.Fatalf("core-a owns %d slots after release, want 342", n)
	}
}

func TestAfterClaimReceivesEachNewlyClaimedSlotOnce(t *testing.T) {
	mr, rdb := newRedis(t)
	claimedA, releasedA, claimedB := &hookLog{}, &hookLog{}, &hookLog{}
	a := newManager(t, rdb, "core-a", withAfterClaim(claimedA.record), withBeforeRelease(releasedA.record))
	claimLeftovers(t, mr, a, crowdedForRelease)
	if batches := claimedA.batches(); len(batches) != 1 || len(batches[0]) != slotmap.Count-crowdedForRelease || !sameSlots(batches[0], a.Owned()) {
		t.Fatalf("first claim batches %v, want one batch of the %d free slots", batchSizes(batches), slotmap.Count-crowdedForRelease)
	}
	stepAll(t, a)
	if n := len(claimedA.batches()); n != 1 {
		t.Fatalf("%d claim batches after a Step that claimed nothing, want still 1", n)
	}
	b := newManager(t, rdb, "core-b", withAfterClaim(claimedB.record))
	stepAll(t, b)
	if n := len(claimedB.batches()); n != 0 {
		t.Fatalf("core-b delivered %d claim batches while live cores hold every slot", n)
	}
	stepAll(t, a, b)
	got, gave := claimedB.batches(), releasedA.batches()
	if len(got) != 1 || len(gave) != 1 || len(got[0]) != 3 || !sameSlots(got[0], gave[0]) || !sameSlots(got[0], b.Owned()) {
		t.Fatalf("core-b claim batches %v, core-a release batches %v, want one identical batch of 3", got, gave)
	}
	assertDisjoint(t, a, b)
}

func TestAfterLoseReportsLeasesOverwrittenByAnotherCore(t *testing.T) {
	mr, rdb := newRedis(t)
	hooks := &hookLog{}
	a := newManager(t, rdb, "core-a", withAfterLose(hooks.tagged("lose")), withAfterClaim(hooks.tagged("claim")))
	claimLeftovers(t, mr, a, crowdedForLoss)
	hooks.reset()
	taken := []uint16{crowdedForLoss + 1, crowdedForLoss + 6}
	overwriteLeases(t, mr, "core-z", taken...)
	hooks.during = func(_ context.Context, tag string, slots []uint16) {
		if tag == "lose" && slices.ContainsFunc(slots, a.Owns) {
			t.Errorf("AfterLose delivered %v while Owns still reports one of them", slots)
		}
	}
	stepAll(t, a)
	batches, tags := hooks.batches(), hooks.tags()
	if !slices.Equal(tags, []string{"lose", "claim"}) || !sameSlots(batches[0], taken) || !sameSlots(batches[1], taken) {
		t.Fatalf("hooks %v with batches %v, want AfterLose then AfterClaim of %v taken back from the dead core", tags, batches, taken)
	}
	if !sameSlots(a.Owned(), slotRange(crowdedForLoss, slotmap.Count)) {
		t.Fatalf("core-a owns %v after taking its slots back", a.Owned())
	}
}

func TestHooksOfOneStepShareOneTimeoutBudget(t *testing.T) {
	mr, rdb := newRedis(t)
	hooks := &hookLog{}
	a := newManager(t, rdb, "core-a", withAfterLose(hooks.record), withAfterClaim(hooks.record))
	claimLeftovers(t, mr, a, crowdedForLoss)
	hooks.reset()
	hooks.block = true
	overwriteLeases(t, mr, "core-z", crowdedForLoss)
	begin := time.Now()
	stepAll(t, a)
	if took := time.Since(begin); took >= a.cfg.Tick {
		t.Fatalf("Step with blocking AfterLose and AfterClaim took %v, want under one tick %v", took, a.cfg.Tick)
	}
	calls := hooks.all()
	if len(calls) != 2 || !calls[0].deadline.Equal(calls[1].deadline) || !nearStepStart(begin, calls[0].deadline, a.cfg.HookTimeout) {
		t.Fatalf("hook calls %+v, want AfterLose and AfterClaim sharing one deadline within HookTimeout of the Step start", calls)
	}
}

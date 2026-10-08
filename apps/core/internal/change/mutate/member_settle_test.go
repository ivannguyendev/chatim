package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type flakyMembers struct {
	*spyMembers
	afterWrite func()
	writeErr   error
	ownersErr  error
}

func (f *flakyMembers) wrote() error {
	if hook := f.afterWrite; hook != nil {
		f.afterWrite = nil
		hook()
	}
	return f.writeErr
}

func (f *flakyMembers) AddMembers(ctx context.Context, j domain.Join, users []string) (store.JoinResult, error) {
	res, err := f.spyMembers.AddMembers(ctx, j, users)
	if werr := f.wrote(); werr != nil {
		return store.JoinResult{}, werr
	}
	return res, err
}

func (f *flakyMembers) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	ok, err := f.spyMembers.ApplyMember(ctx, cur, next)
	if werr := f.wrote(); werr != nil {
		return false, werr
	}
	return ok, err
}

func (f *flakyMembers) ChangeOwners(ctx context.Context, room uint64, users []string, decide store.OwnerDecision) (store.OwnerResult, error) {
	res, err := f.spyMembers.ChangeOwners(ctx, room, users, decide)
	if f.ownersErr != nil {
		return store.OwnerResult{}, f.ownersErr
	}
	return res, err
}

type deadlineTimers struct{ *fakeTimers }

func (d deadlineTimers) Disarm(ctx context.Context, t work.Timer) {
	if ctx.Err() != nil {
		d.log.add("disarm on a dead context")
		return
	}
	d.fakeTimers.Disarm(ctx, t)
}

func (rg *rig) flaky(t *testing.T) *flakyMembers {
	t.Helper()
	f := &flakyMembers{spyMembers: rg.members}
	d := rg.deps(t, nil)
	d.Members, d.Timers = f, deadlineTimers{rg.timers}
	rg.m = rg.build(t, d)
	return f
}

func (rg *rig) requestStatus(t *testing.T, user, requestID string) dedupe.RequestStatus {
	t.Helper()
	status, err := newRequests(t, rg.registry).Begin(t.Context(), dedupe.RequestKey(group, user, requestID))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return status
}

func TestAMemberWriteWithAnUnknownOutcomeForgetsTheMembers(t *testing.T) {
	rg := newMemberRig(t, nil)
	f := rg.flaky(t)
	f.writeErr = errBoom
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "zoe")); !errors.Is(err, errBoom) {
		t.Fatalf("AddMembers = %v, want the write error", err)
	}
	if _, err := rg.m.RemoveMember(t.Context(), remove("owen", "max")); !errors.Is(err, errBoom) {
		t.Fatalf("RemoveMember = %v, want the write error", err)
	}
	f.ownersErr = errBoom
	if _, err := rg.m.LeaveRoom(t.Context(), leave("owen")); !errors.Is(err, errBoom) {
		t.Fatalf("LeaveRoom of the owner = %v, want the transaction error", err)
	}
	if got := rg.forgets.list(); !slices.Equal(got, []uint64{group, group, group}) {
		t.Fatalf("forgot %v, want the group once per failed write", got)
	}
}

func TestARetryThatFindsItsOwnJoinForgetsTheMembers(t *testing.T) {
	rg := newMemberRig(t, nil)
	if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "zoe")); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	rg.redis.FlushAll()
	rg.restart(t)
	added, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "zoe"))
	if err != nil || len(added) != 1 || added[0].User != "zoe" {
		t.Fatalf("AddMembers retry = %+v, %v; want zoe", added, err)
	}
	if got := rg.forgets.list(); !slices.Equal(got, []uint64{group, group}) {
		t.Fatalf("forgot %v, want the group after the write and after the retry", got)
	}
}

func TestACancelledRequestStillSettlesItsRequestAndTimer(t *testing.T) {
	rg := newMemberRig(t, nil)
	f := rg.flaky(t)
	ctx, cancel := context.WithCancel(t.Context())
	f.afterWrite = cancel
	if _, err := rg.m.AddMembers(ctx, add("owen", "r1", "zoe")); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if got := rg.requestStatus(t, "owen", "r1"); got != dedupe.RequestDone {
		t.Fatalf("request after a cancelled context = %v, want done", got)
	}
	ctx, cancel = context.WithCancel(t.Context())
	f.afterWrite = cancel
	if _, err := rg.m.RemoveMember(ctx, remove("owen", "max")); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if n := rg.timers.pending(); n != 0 {
		t.Fatalf("armed timers = %d, calls %v; want every timer disarmed", n, rg.calls.list())
	}
	if c := rg.memberCount(t); c.Count != 4 {
		t.Fatalf("member count = %+v, want 4 after one join and one removal", c)
	}
	ctx, cancel = context.WithCancel(t.Context())
	f.afterWrite, f.writeErr = cancel, errBoom
	if _, err := rg.m.AddMembers(ctx, add("owen", "r2", "kim")); !errors.Is(err, errBoom) {
		t.Fatalf("AddMembers = %v, want the write error", err)
	}
	if got := rg.requestStatus(t, "owen", "r2"); got != dedupe.RequestNew {
		t.Fatalf("request after a failed write on a cancelled context = %v, want released", got)
	}
}

func TestASlowMemberWriteStillLeavesTheWholeSettleBudget(t *testing.T) {
	rg := newMemberRig(t, nil)
	f := rg.flaky(t)
	synctest.Test(t, func(t *testing.T) {
		f.afterWrite = func() { time.Sleep(2500 * time.Millisecond) }
		if _, err := rg.m.AddMembers(t.Context(), add("owen", "r1", "zoe")); err != nil {
			t.Fatalf("AddMembers: %v", err)
		}
	})
	if got := rg.requestStatus(t, "owen", "r1"); got != dedupe.RequestDone {
		t.Fatalf("request after a slow write = %v, want done", got)
	}
	if n := rg.timers.pending(); n != 0 {
		t.Fatalf("armed timers = %d, calls %v; want the timer disarmed", n, rg.calls.list())
	}
	if got := rg.memberEvents(); !slices.Equal(got, []string{"added zoe", "count 5"}) {
		t.Fatalf("events = %v, want the join and the new count", got)
	}
}

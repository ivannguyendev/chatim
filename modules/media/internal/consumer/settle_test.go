package consumer_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errDown = errors.New("storage down")

func withFile(now time.Time) *chatimv1.Event { return created(41, now, 1) }

func TestSettleBranches(t *testing.T) {
	cases := map[string]struct {
		err       error
		ev        func(time.Time) *chatimv1.Event
		raw       []byte
		delivered uint64
		want      settledAs
		invalid   uint64
		stuck     uint64
		synced    bool
	}{
		"done acks":         {ev: withFile, delivered: 1, want: settledAs{acks: 1}, synced: true},
		"dropped acks":      {err: attach.ErrDropped, ev: withFile, delivered: 1, want: settledAs{acks: 1}, synced: true},
		"undecodable terms": {raw: []byte{0xff, 0xff, 0xff}, delivered: 1, want: settledAs{terms: 1}, invalid: 1},
		"bad room terms":    {ev: func(now time.Time) *chatimv1.Event { ev := withFile(now); ev.RoomId = "x"; return ev }, delivered: 1, want: settledAs{terms: 1}, invalid: 1},
		"no ts terms":       {ev: func(now time.Time) *chatimv1.Event { ev := withFile(now); ev.Ts = nil; return ev }, delivered: 1, want: settledAs{terms: 1}, invalid: 1},
		"other payload terms": {ev: func(now time.Time) *chatimv1.Event {
			ev := envelope(1, now)
			ev.Payload = &chatimv1.Event_RoomCreated{}
			return ev
		}, delivered: 1, want: settledAs{terms: 1}, invalid: 1},
		"invalid argument terms":             {err: fmt.Errorf("ref: %w", apperr.ErrInvalidArgument), ev: withFile, delivered: 1, want: settledAs{terms: 1}, invalid: 1, synced: true},
		"waiting upload naks, not stuck":     {err: attach.ErrWaitUpload, ev: withFile, delivered: 6, want: settledAs{naks: []time.Duration{10 * time.Minute}}, synced: true},
		"failure naks first":                 {err: errDown, ev: withFile, delivered: 1, want: settledAs{naks: []time.Duration{5 * time.Second}}, synced: true},
		"lost race naks on the ladder":       {err: domain.ErrRetryLater, ev: withFile, delivered: 3, want: settledAs{naks: []time.Duration{2 * time.Minute}}, synced: true},
		"sixth failed delivery is stuck":     {err: errDown, ev: withFile, delivered: 6, want: settledAs{naks: []time.Duration{10 * time.Minute}}, stuck: 1, synced: true},
		"deleted message goes to the syncer": {ev: func(now time.Time) *chatimv1.Event { return deleted(41, now) }, delivered: 1, want: settledAs{acks: 1}, synced: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig()
				r.sync.err = tc.err
				d := &fakeDelivery{data: tc.raw, delivered: tc.delivered}
				if tc.ev != nil {
					d = delivery(t, tc.ev(time.Now()), tc.delivered)
				}
				c := runOnce(t, r, cfg(), d)
				if got := settledOf(d); got.acks != tc.want.acks || got.terms != tc.want.terms || !slices.Equal(got.naks, tc.want.naks) {
					t.Errorf("settled %+v, want %+v", got, tc.want)
				}
				if r.stats.Dropped(attach.ReasonInvalid) != tc.invalid || c.Stuck() != tc.stuck {
					t.Errorf("invalid %d stuck %d, want %d %d", r.stats.Dropped(attach.ReasonInvalid), c.Stuck(), tc.invalid, tc.stuck)
				}
				if synced := len(r.sync.recorded()) > 0; synced != tc.synced {
					t.Errorf("synced %v, want %v", synced, tc.synced)
				}
			})
		})
	}
}

func TestEventsDecodeIntoTheMessageRefAndKind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		now := time.Now()
		threaded := created(5, now, 2)
		threaded.RoomId, threaded.ThreadRoot = "0777", 3
		runOnce(t, r, cfg(), delivery(t, threaded, 1), delivery(t, created(6, now, 0), 1), delivery(t, deleted(7, now), 1))
		calls := r.sync.recorded()
		slices.SortFunc(calls, func(a, b syncCall) int { return int(a.ev.Ref.Seq) - int(b.ev.Ref.Seq) })
		want := []syncCall{
			{attach.Created, attach.Event{Tenant: tenant, Ref: domain.MessageRef{Room: "777", Thread: 3, Seq: 5}, HasFiles: true}},
			{attach.Created, attach.Event{Tenant: tenant, Ref: domain.MessageRef{Room: "777", Seq: 6}}},
			{attach.Deleted, attach.Event{Tenant: tenant, Ref: domain.MessageRef{Room: "777", Seq: 7}}},
		}
		if !slices.Equal(calls, want) {
			t.Fatalf("calls %+v", calls)
		}
	})
}

func TestASyncPastItsTimeoutNaksOnTheLadder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.delay = 20 * time.Minute
		d := delivery(t, withFile(time.Now()), 1)
		begin := time.Now()
		c := runOnce(t, r, cfg(), d)
		if took := time.Since(begin); took != cfg().SyncTimeout {
			t.Errorf("sync ran %v, want the %v timeout", took, cfg().SyncTimeout)
		}
		if got := settledOf(d); !slices.Equal(got.naks, []time.Duration{5 * time.Second}) || got.acks != 0 || c.Stuck() != 0 {
			t.Errorf("settled %+v stuck %d", got, c.Stuck())
		}
		if calls, late := d.progressed(); calls < 80 || late != 0 {
			t.Errorf("in progress %d late %d, want a signal every 10s and none after settling", calls, late)
		}
	})
}

func TestSettleNaksZeroWhenRunEndsMidEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		d := delivery(t, withFile(time.Now()), 1)
		q := &fakeQueue{}
		q.push(d)
		ctx, cancel := context.WithCancel(t.Context())
		run := start(ctx, r.consumer(t, q, cfg()))
		synctest.Wait()
		cancel()
		if err := <-run.done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		if got := settledOf(d); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{0}) {
			t.Errorf("settled %+v, want one Nak(0)", got)
		}
	})
}

func TestLagFollowsTheOldestEventAndDropsWhenIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.sync.block = make(chan struct{})
		now := time.Now()
		q := &fakeQueue{}
		q.push(delivery(t, withFile(now.Add(-2*time.Second)), 1), delivery(t, deleted(42, now), 1))
		c := r.consumer(t, q, cfg())
		run := start(t.Context(), c)
		synctest.Wait()
		if c.Lag() != 2*time.Second {
			t.Errorf("lag %v, want 2s", c.Lag())
		}
		close(r.sync.block)
		synctest.Wait()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if c.Lag() != 0 {
			t.Errorf("lag after an empty fetch %v, want 0", c.Lag())
		}
		run.stop(t)
	})
}

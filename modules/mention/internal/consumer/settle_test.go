package consumer_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errDown = errors.New("core down")

type settledAs struct {
	acks, terms int
	naks        []time.Duration
}

func settledOf(d *fakeDelivery) settledAs {
	acks, terms, naks := d.settled()
	return settledAs{acks, terms, naks}
}

func TestSettleBranches(t *testing.T) {
	cases := map[string]struct {
		setup       func(*rig)
		ev          func(time.Time) *chatimv1.Event
		raw         []byte
		delivered   uint64
		want        settledAs
		dropped     uint64
		failures    uint64
		resolverHit bool
	}{
		"done acks":                {ev: mentionMinh, delivered: 1, want: settledAs{acks: 1}, resolverHit: true},
		"no targets acks":          {ev: func(now time.Time) *chatimv1.Event { return created(41, now) }, delivered: 1, want: settledAs{acks: 1}},
		"gone room acks":           {setup: func(r *rig) { r.resolver.err = fmt.Errorf("room: %w", apperr.ErrNotFound) }, ev: mentionMinh, delivered: 1, want: settledAs{acks: 1}, dropped: 1, resolverHit: true},
		"undecodable terms":        {raw: []byte{0xff, 0xff, 0xff}, delivered: 1, want: settledAs{terms: 1}, dropped: 1},
		"invalid event terms":      {ev: func(now time.Time) *chatimv1.Event { ev := mentionMinh(now); ev.RoomId = "x"; return ev }, delivered: 1, want: settledAs{terms: 1}, dropped: 1},
		"unbuildable notice terms": {setup: func(r *rig) { r.resolver.groups["sale"] = []string{"Minh.x"} }, ev: func(now time.Time) *chatimv1.Event { return created(41, now, group("sale", now)) }, delivered: 1, want: settledAs{terms: 1}, dropped: 1, resolverHit: true},
		"failure naks first":       {setup: func(r *rig) { r.resolver.err = errDown }, ev: mentionMinh, delivered: 1, want: settledAs{naks: []time.Duration{5 * time.Second}}, failures: 1, resolverHit: true},
		"failure climbs":           {setup: func(r *rig) { r.resolver.err = errDown }, ev: mentionMinh, delivered: 3, want: settledAs{naks: []time.Duration{2 * time.Minute}}, failures: 1, resolverHit: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig()
				if tc.setup != nil {
					tc.setup(r)
				}
				d := &fakeDelivery{data: tc.raw, delivered: tc.delivered}
				if tc.ev != nil {
					d = delivery(t, tc.ev(time.Now()), tc.delivered)
				}
				c := runOnce(t, r, cfg(), d)
				if got := settledOf(d); got.acks != tc.want.acks || got.terms != tc.want.terms || !slices.Equal(got.naks, tc.want.naks) {
					t.Errorf("settled %+v, want %+v", got, tc.want)
				}
				if c.Dropped() != tc.dropped || c.Failures() != tc.failures {
					t.Errorf("dropped %d failures %d, want %d %d", c.Dropped(), c.Failures(), tc.dropped, tc.failures)
				}
				if hit := r.resolver.calls > 0; hit != tc.resolverHit {
					t.Errorf("resolver called %v, want %v", hit, tc.resolverHit)
				}
			})
		})
	}
}

func mentionMinh(now time.Time) *chatimv1.Event { return created(41, now, user("Minh", now)) }

func TestSettleNaksZeroWhenRunEndsMidEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.block = make(chan struct{})
		d := delivery(t, mentionMinh(time.Now()), 1)
		q := &fakeQueue{}
		q.push(d)
		ctx, cancel := context.WithCancel(t.Context())
		c := r.consumer(t, q, cfg())
		run := start(ctx, c)
		synctest.Wait()
		cancel()
		if err := <-run.done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		if got := settledOf(d); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{0}) || c.Failures() != 0 {
			t.Errorf("settled %+v failures %d, want one Nak(0) and no failure", got, c.Failures())
		}
	})
}

func TestStuckAndLagProbes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.block = make(chan struct{})
		now := time.Now()
		d := delivery(t, created(41, now.Add(-2*time.Second), user("Minh", now)), 6)
		q := &fakeQueue{}
		q.push(d)
		c := r.consumer(t, q, cfg())
		run := start(t.Context(), c)
		synctest.Wait()
		if c.Stuck() != 1 || c.Lag() != 2*time.Second {
			t.Errorf("stuck %d lag %v, want 1 and 2s", c.Stuck(), c.Lag())
		}
		close(r.resolver.block)
		time.Sleep(cfg().FetchWait)
		synctest.Wait()
		if c.Lag() != 0 {
			t.Errorf("lag after an empty fetch %v, want 0", c.Lag())
		}
		run.stop(t)
		if c.Name() != "mention" {
			t.Errorf("name %q", c.Name())
		}
	})
}

package consumer_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/notify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func liveTargets(t *testing.T, r *rig, seq uint64) []string {
	t.Helper()
	docs, err := r.mentions.Of(t.Context(), tenant, key(seq))
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	var out []string
	for _, m := range docs {
		if m.State == domain.StateLive {
			out = append(out, m.Target.Name(777))
		}
	}
	slices.Sort(out)
	return out
}

func TestCreatedNotifiesEveryRecipientAndRaisesSharedTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.groups["sale"] = []string{"Lan", "Hoa", "Minh"}
		now := time.Now()
		d := delivery(t, created(41, now, user("Minh", now), group("sale", now)), 1)
		runOnce(t, r, cfg(), d)
		if acks, _, _ := d.settled(); acks != 1 {
			t.Fatalf("acks %d, want 1", acks)
		}
		want := []string{"notify.t1.Hoa.mention 777-0-41-Hoa-m0", "notify.t1.Minh.mention 777-0-41-Minh-m0"}
		if got := r.js.published(); !slices.Equal(got, want) {
			t.Errorf("published %v, want %v", got, want)
		}
		if got := liveTargets(t, r, 41); !slices.Equal(got, []string{"group:sale", "user:Minh"}) {
			t.Errorf("live mentions %v", got)
		}
		times, _ := r.rooms.Times(t.Context(), tenant, []uint64{777})
		if !times[777].Equal(now) {
			t.Errorf("shared mention time %v, want %v", times[777], now)
		}
	})
}

func TestEditNotifiesAddedAndRetractsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		now := time.Now()
		d := delivery(t, edited(41, 1, now, []*chatimv1.MentionTarget{user("Hoa", now)}, user("Minh", now.Add(-time.Minute))), 1)
		runOnce(t, r, cfg(), d)
		want := []string{"notify.t1.Hoa.mention 777-0-41-Hoa-m1", "notify.t1.Minh.mention 777-0-41-Minh-r1"}
		if got := r.js.published(); !slices.Equal(got, want) {
			t.Errorf("published %v, want %v", got, want)
		}
		if got := liveTargets(t, r, 41); !slices.Equal(got, []string{"user:Hoa"}) {
			t.Errorf("live mentions %v", got)
		}
		if times, _ := r.rooms.Times(t.Context(), tenant, []uint64{777}); len(times) != 0 {
			t.Errorf("shared time raised %v by user mentions only", times)
		}
	})
}

func TestStaleAndSupersededWriteMentionsButPublishNothing(t *testing.T) {
	cases := map[string]struct {
		age     time.Duration
		current uint32
		reason  string
	}{
		"stale":      {age: 5*time.Minute + time.Second, reason: notify.ReasonStale},
		"superseded": {current: 2, reason: notify.ReasonSuperseded},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig()
				r.versions.current[key(41)] = tc.current
				at := time.Now().Add(-tc.age)
				d := delivery(t, edited(41, 1, at, []*chatimv1.MentionTarget{user("Hoa", at)}), 1)
				c := runOnce(t, r, cfg(), d)
				if acks, _, _ := d.settled(); acks != 1 {
					t.Fatalf("acks %d, want 1", acks)
				}
				if got := r.js.published(); len(got) != 0 {
					t.Errorf("published %v, want nothing", got)
				}
				if got := liveTargets(t, r, 41); !slices.Equal(got, []string{"user:Hoa"}) {
					t.Errorf("live mentions %v, want the write kept", got)
				}
				if c.Stats().Skipped(tc.reason) != 1 || r.resolver.calls != 0 {
					t.Errorf("skipped %s %d, resolver calls %d", tc.reason, c.Stats().Skipped(tc.reason), r.resolver.calls)
				}
			})
		})
	}
}

func TestPublishFailureNaksAndTheReplayIsIdempotent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.js.failing(true)
		now := time.Now()
		ev := mentionMinh(now)
		first := delivery(t, ev, 1)
		c := runOnce(t, r, cfg(), first)
		if got := settledOf(first); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{5 * time.Second}) {
			t.Fatalf("first settled %+v, want Nak(5s)", got)
		}
		before, _ := r.mentions.Of(t.Context(), tenant, key(41))
		if len(before) != 1 || c.Failures() != 1 || c.Stats().Failures() != 1 {
			t.Fatalf("mentions %v failures %d/%d, want the write kept and one failure", before, c.Failures(), c.Stats().Failures())
		}
		r.js.failing(false)
		second := delivery(t, ev, 2)
		c = runOnce(t, r, cfg(), second)
		if acks, _, _ := second.settled(); acks != 1 || c.Stats().Notified() != 1 {
			t.Fatalf("replay acks %d notified %d, want 1 and 1", acks, c.Stats().Notified())
		}
		after, _ := r.mentions.Of(t.Context(), tenant, key(41))
		if !slices.Equal(before, after) {
			t.Errorf("replay changed mentions %v -> %v", before, after)
		}
	})
}

func TestOneVersionReadPerBatchAndAFailedReadNaks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		now := time.Now()
		ds := []*fakeDelivery{
			delivery(t, mentionMinh(now), 1),
			delivery(t, edited(41, 1, now, []*chatimv1.MentionTarget{user("Hoa", now)}), 1),
			delivery(t, created(42, now, user("Hoa", now)), 1),
			delivery(t, created(43, now), 1),
		}
		runOnce(t, r, cfg(), ds...)
		calls := r.versions.calls()
		if len(calls) != 1 || len(calls[0]) != 2 {
			t.Fatalf("version reads %v, want one read of keys 41 and 42", calls)
		}
		r2 := newRig()
		r2.versions.err = errDown
		d := delivery(t, mentionMinh(now), 1)
		runOnce(t, r2, cfg(), d)
		if got := settledOf(d); !slices.Equal(got.naks, []time.Duration{5 * time.Second}) || len(liveTargets(t, r2, 41)) != 1 {
			t.Errorf("settled %+v, want Nak(5s) with the mention written", got)
		}
	})
}

package consumer_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func deleted(seq uint64, ver uint32, at time.Time, previous ...*chatimv1.MentionTarget) *chatimv1.Event {
	ev := envelope(seq, at)
	msg := message(seq, ver, at)
	msg.Deleted = true
	ev.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{
		Message: msg, Ver: ver, PreviousMentions: &chatimv1.MentionSet{Targets: previous},
	}}
	return ev
}

func targetsIn(t *testing.T, r *rig, seq uint64, state domain.State) []string {
	t.Helper()
	docs, err := r.mentions.Of(t.Context(), tenant, key(seq))
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	var out []string
	for _, m := range docs {
		if m.State == state {
			out = append(out, m.Target.Name(777))
		}
	}
	slices.Sort(out)
	return out
}

func TestEditRetiresThePreviousTargetsItDrops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		now := time.Now()
		d := delivery(t, edited(41, 1, now, []*chatimv1.MentionTarget{user("Hoa", now)}, user("Minh", now), user("Hoa", now)), 1)
		runOnce(t, r, cfg(), d)
		if got := targetsIn(t, r, 41, domain.StateRetired); !slices.Equal(got, []string{"user:Minh"}) {
			t.Errorf("retired %v, want the dropped previous target", got)
		}
		if got := r.js.published(); !slices.Equal(got, []string{"websocket.publish.t1 777-0-41-r1-edited Minh"}) {
			t.Errorf("published %v, want only the retraction of the dropped target", got)
		}
	})
}

func TestDeleteWithPreviousTargetsRetiresAndRetractsThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.groups["sale"] = []string{"Lan", "Hoa", "Minh"}
		now := time.Now()
		d := delivery(t, deleted(41, 2, now, user("Minh", now), group("sale", now)), 1)
		runOnce(t, r, cfg(), d)
		if got := settledOf(d); got.acks != 1 {
			t.Fatalf("settled %+v, want one ack", got)
		}
		if got := targetsIn(t, r, 41, domain.StateRetired); !slices.Equal(got, []string{"group:sale", "user:Minh"}) {
			t.Errorf("retired %v, want a tombstone per previous target", got)
		}
		if got := targetsIn(t, r, 41, domain.StateLive); len(got) != 0 {
			t.Errorf("live %v after a delete", got)
		}
		want := []string{"websocket.publish.t1 777-0-41-r2-deleted Hoa,Minh"}
		if got := r.js.published(); !slices.Equal(got, want) {
			t.Errorf("published %v, want %v", got, want)
		}
	})
}

func TestDeleteWithNoPreviousTargetsAcksWithoutWrites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		d := delivery(t, deleted(41, 2, time.Now()), 1)
		runOnce(t, r, cfg(), d)
		if got := settledOf(d); got.acks != 1 || got.terms != 0 || len(got.naks) != 0 {
			t.Errorf("settled %+v, want one ack", got)
		}
		if n := r.mentions.count(key(41)); n != 0 || len(r.js.published()) != 0 || len(r.versions.calls()) != 0 || r.resolver.calls != 0 {
			t.Errorf("applied %d published %v version reads %d resolver %d, want nothing", n, r.js.published(), len(r.versions.calls()), r.resolver.calls)
		}
	})
}

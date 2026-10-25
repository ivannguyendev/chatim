package consumer_test

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func members(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("u%04d", i)
	}
	return out
}

func pageIDs(ds []*chatimv1.WsPublish) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.GetId()
	}
	return out
}

func recipientsOf(t *testing.T, ds []*chatimv1.WsPublish) []string {
	t.Helper()
	var out []string
	for _, d := range ds {
		if d.GetNotification().GetUser() != "" || d.GetKind() != "mention" || d.GetTenant() != tenant {
			t.Fatalf("packet %s names user %q kind %q tenant %q", d.GetId(), d.GetNotification().GetUser(), d.GetKind(), d.GetTenant())
		}
		out = append(out, d.GetTargets().GetUsers()...)
	}
	slices.Sort(out)
	return out
}

func TestAGroupOfManyIsSentAsPagesNamingEveryoneOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.groups["big"] = members(2500)
		now := time.Now()
		d := delivery(t, created(41, now, group("big", now)), 1)
		runOnce(t, r, cfg(), d)
		if acks, _, _ := d.settled(); acks != 1 {
			t.Fatalf("acks %d, want 1", acks)
		}
		ds := r.js.wsPackets()
		if len(ds) != 3 {
			t.Fatalf("packets %d, want 3", len(ds))
		}
		for i, p := range ds {
			if p.GetNotification().GetId() != "777-0-41-m0-g:big" || p.GetPart() != uint32(i+1) || p.GetParts() != 3 {
				t.Errorf("packet %d is %s part %d/%d", i, p.GetNotification().GetId(), p.GetPart(), p.GetParts())
			}
		}
		if got := recipientsOf(t, ds); !slices.Equal(got, members(2500)) {
			t.Errorf("recipients %d, want each of the 2,500 members once", len(got))
		}
	})
}

func TestARerunReusesUnchangedPagesAndLosesNobodyAfterALeave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.groups["big"] = members(2500)
		now := time.Now()
		ev := created(41, now, group("big", now))
		runOnce(t, r, cfg(), delivery(t, ev, 1))
		first := pageIDs(r.js.wsPackets())
		c := runOnce(t, r, cfg(), delivery(t, ev, 2))
		if again := pageIDs(r.js.wsPackets())[3:]; !slices.Equal(again, first) || c.Stats().Notified() != 0 {
			t.Fatalf("rerun ids %v notified %d, want %v and nothing new stored", again, c.Stats().Notified(), first)
		}
		r.resolver.groups["big"] = slices.Delete(members(2500), 2400, 2401)
		c = runOnce(t, r, cfg(), delivery(t, ev, 3))
		after := r.js.wsPackets()[6:]
		ids := pageIDs(after)
		if ids[0] != first[0] || ids[1] != first[1] || ids[2] == first[2] {
			t.Fatalf("ids after a leave %v, first %v; want only the last page renamed", ids, first)
		}
		if got := recipientsOf(t, after); len(got) != 2499 || slices.Contains(got, "u2400") || c.Stats().Notified() != 499 {
			t.Errorf("recipients %d notified %d, want 2,499 named and the 499 of the new page stored", len(got), c.Stats().Notified())
		}
	})
}

func TestOneFailedPageNaksTheWholeEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig()
		r.resolver.groups["big"] = members(2500)
		r.js.failingPart(2)
		now := time.Now()
		d := delivery(t, created(41, now, group("big", now)), 1)
		c := runOnce(t, r, cfg(), d)
		if got := settledOf(d); got.acks != 0 || !slices.Equal(got.naks, []time.Duration{5 * time.Second}) {
			t.Fatalf("settled %+v, want Nak(5s)", got)
		}
		if c.Stats().Failures() != 1 || c.Stats().Notified() != 1500 || len(r.js.wsPackets()) != 3 {
			t.Errorf("failures %d notified %d packets %d, want one failure, 1,500 stored, 3 sent", c.Stats().Failures(), c.Stats().Notified(), len(r.js.wsPackets()))
		}
	})
}

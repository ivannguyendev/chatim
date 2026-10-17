package plan_test

import (
	"maps"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/plan"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestDiff(t *testing.T) {
	current := map[string]domain.Target{"Ha": user("Ha", editedAt), "Minh": all(sentAt)}
	previous := map[string]domain.Target{"Minh": user("Minh", sentAt), "Tu": group("team-sale", sentAt)}
	notify, retract := plan.Diff(current, previous)
	if want := map[string]domain.Target{"Ha": user("Ha", editedAt)}; !maps.Equal(notify, want) {
		t.Fatalf("notify = %v, want %v", notify, want)
	}
	if want := map[string]domain.Target{"Tu": group("team-sale", sentAt)}; !maps.Equal(retract, want) {
		t.Fatalf("retract = %v, want %v", retract, want)
	}
	notify, retract = plan.Diff(nil, nil)
	if len(notify) != 0 || len(retract) != 0 || notify == nil || retract == nil {
		t.Fatalf("Diff(nil, nil) = %#v, %#v; want empty maps", notify, retract)
	}
}

type notice struct {
	ver     uint32
	retract bool
}

func TestHighestNoticePerUserIsTheSameInEveryOrder(t *testing.T) {
	set := func(ids ...string) *chatimv1.MentionSet {
		s := &chatimv1.MentionSet{}
		for _, id := range ids {
			s.Targets = append(s.Targets, userTarget(id, ts(sentAt)))
		}
		return s
	}
	events := []*chatimv1.Event{
		created(message(0, userTarget("Minh", ts(sentAt)), userTarget("Ha", ts(sentAt)))),
		edited(1, message(1, userTarget("Ha", ts(sentAt))), set("Minh", "Ha")),
		edited(2, message(2, userTarget("Ha", ts(sentAt)), userTarget("Minh", ts(editedAt))), set("Ha")),
		deleted(3, set("Ha", "Minh")),
	}
	for _, order := range orders(len(events)) {
		latest := map[string]notice{}
		for _, i := range order {
			r, err := plan.FromEvent(events[i])
			if err != nil {
				t.Fatalf("FromEvent(%d): %v", i, err)
			}
			notify, retract := plan.Diff(byUser(r.Current), byUser(r.Previous))
			keep(latest, notify, r.Plan.Ver, false)
			keep(latest, retract, r.Plan.Ver, true)
		}
		want := map[string]notice{"Minh": {ver: 3, retract: true}, "Ha": {ver: 3, retract: true}}
		if !maps.Equal(latest, want) {
			t.Fatalf("order %v: latest notices = %v, want %v", order, latest, want)
		}
	}
}

func byUser(targets []domain.Target) map[string]domain.Target {
	out := map[string]domain.Target{}
	for _, t := range targets {
		out[t.ID] = t
	}
	return out
}

func keep(latest map[string]notice, users map[string]domain.Target, ver uint32, retract bool) {
	for u := range users {
		if cur, ok := latest[u]; !ok || cur.ver < ver {
			latest[u] = notice{ver: ver, retract: retract}
		}
	}
}

func orders(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, rest := range orders(n - 1) {
		for at := 0; at <= len(rest); at++ {
			p := append(append(append(make([]int, 0, n), rest[:at]...), n-1), rest[at:]...)
			out = append(out, p)
		}
	}
	return out
}

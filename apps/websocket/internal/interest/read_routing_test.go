package interest

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
)

func TestReadPositionsReachViewersAndEveryConnectionOfListedMembersOnce(t *testing.T) {
	cases := map[string]struct {
		users []string
		want  []string
	}{
		"a listed member that is not viewing": {[]string{"ow"}, []string{"ow", "va1"}},
		"a listed viewer gets one frame":      {[]string{"va"}, []string{"va1", "va2"}},
		"several listed members":              {[]string{"mb", "ow", "va"}, []string{"mb", "ow", "va1", "va2"}},
		"a listed user outside the room":      {[]string{"out"}, []string{"va1"}},
		"a listed user nobody knows":          {[]string{"ghost"}, []string{"va1"}},
		"the same user listed twice":          {[]string{"ow", "ow"}, []string{"ow", "va1"}},
		"no entries":                          {nil, []string{"va1"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			run(t, func(t *testing.T) {
				a := newAudience(t)
				a.route(reads("t1", 1, c.users...))
				if got := a.receivers(); !slices.Equal(got, c.want) {
					t.Fatalf("read_positions listing %v reached %v, want %v", c.users, got, c.want)
				}
			})
		})
	}
}

func TestReadPositionsSkipAListedUserRemovedFromTheRoom(t *testing.T) {
	run(t, func(t *testing.T) {
		a := newAudience(t)
		a.route(removed("t1", 1, "ow", 2))
		a.receivers()
		a.route(reads("t1", 1, "ow", "mb"))
		if got := a.receivers(); !slices.Equal(got, []string{"mb", "va1"}) {
			t.Fatalf("read_positions reached %v, want mb and the viewer, not the removed ow", got)
		}
	})
}

func TestReadPositionsNeverCrossTenantsAndDropMalformedPayloads(t *testing.T) {
	run(t, func(t *testing.T) {
		a := newAudience(t)
		a.route(reads("t2", 1, "ow"))
		if got := a.receivers(); !slices.Equal(got, []string{"t2ow"}) {
			t.Fatalf("t2 read_positions reached %v", got)
		}
		wrongPayload := event("history_cleared", "t1", 1, "ow", 0)
		wrongPayload.Kind = "read_positions"
		otherRoom := reads("t1", 2, "ow")
		otherRoom.Room = "1"
		garbage := reads("t1", 1, "ow")
		garbage.Data = []byte{0xff, 0xff}
		for _, env := range []feed.Envelope{wrongPayload, otherRoom, garbage} {
			a.route(env)
		}
		if got := a.receivers(); len(got) != 0 {
			t.Fatalf("malformed read_positions reached %v", got)
		}
		if n := a.table.stats.dropped[dropMalformed].Load(); n != 3 {
			t.Fatalf("dropped malformed = %d, want 3", n)
		}
	})
}

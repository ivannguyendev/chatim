package interest

import (
	"fmt"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
)

type when int

const (
	beforeRegister when = iota
	duringLoad
	afterReady
)

const (
	markSeq   = 100
	orderRoom = 7
)

type timed struct {
	env  feed.Envelope
	when when
}

type orderCase struct {
	name  string
	list  []Room
	steps []timed
}

func orderCases(events []feed.Envelope, lists [][]Room) []orderCase {
	var out []orderCase
	for reflected, list := range lists {
		for lowMask := range 1 << len(events) {
			seqs, ok := seqsFor(lowMask, len(events), reflected)
			if !ok {
				continue
			}
			for _, whens := range whensFor(seqs) {
				c := orderCase{name: fmt.Sprintf("list%d/seqs%v/when%v", reflected, seqs, whens), list: list}
				for i, env := range events {
					c.steps = append(c.steps, timed{env: withSeq(env, seqs[i]), when: whens[i]})
				}
				out = append(out, c)
			}
		}
	}
	return out
}

func seqsFor(lowMask, n, reflected int) ([]uint64, bool) {
	seqs := make([]uint64, n)
	for i := range n {
		low := lowMask&(1<<i) != 0
		if low && i >= reflected {
			return nil, false
		}
		seqs[i] = uint64(markSeq + 10*(i+1))
		if low {
			seqs[i] = uint64(10 * (i + 1))
		}
		if i > 0 && seqs[i] < seqs[i-1] {
			return nil, false
		}
	}
	return seqs, true
}

func whensFor(seqs []uint64) [][]when {
	var out [][]when
	var walk func(i int, from when, acc []when)
	walk = func(i int, from when, acc []when) {
		if i == len(seqs) {
			out = append(out, slices.Clone(acc))
			return
		}
		for w := from; w <= afterReady; w++ {
			if w == beforeRegister && seqs[i] > markSeq {
				continue
			}
			walk(i+1, w, append(acc, w))
		}
	}
	walk(0, beforeRegister, nil)
	return out
}

func playOrder(t *testing.T, c orderCase) (member bool, pairs int64) {
	f := newFixture(t)
	f.loader.holding(true)
	f.route(withSeq(msg("t1", 99), markSeq))
	routeAt := func(w when) {
		for _, s := range c.steps {
			if s.when == w {
				f.route(s.env)
			}
		}
	}
	routeAt(beforeRegister)
	sink := &fakeSink{}
	f.table.Register("t1", "u", sink)
	synctest.Wait()
	routeAt(duringLoad)
	f.loader.replies <- reply{rooms: c.list}
	synctest.Wait()
	if got := sink.take(); !slices.Equal(got, []string{"ready"}) {
		t.Fatalf("frames after the load = %v, want ready only", got)
	}
	routeAt(afterReady)
	sink.take()
	f.route(msg("t1", orderRoom))
	return slices.Contains(sink.take(), fmt.Sprintf("event msg_created/t1/%d//v0", orderRoom)), f.table.stats.pairs.Load()
}

func TestEveryOrderOfLoadAndRemovalEndsOutsideTheRoom(t *testing.T) {
	events := []feed.Envelope{added("t1", orderRoom, "u", 1), removed("t1", orderRoom, "u", 2)}
	lists := [][]Room{nil, {{ID: orderRoom, Ver: 1}}, nil}
	cases := orderCases(events, lists)
	if len(cases) < 20 {
		t.Fatalf("only %d orders generated", len(cases))
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				if member, pairs := playOrder(t, c); member || pairs != 0 {
					t.Fatalf("member = %v, pairs = %d; want outside the room", member, pairs)
				}
			})
		})
	}
}

func TestEveryOrderOfLoadAndAddingEndsInsideTheRoom(t *testing.T) {
	events := []feed.Envelope{added("t1", orderRoom, "u", 1)}
	lists := [][]Room{nil, {{ID: orderRoom, Ver: 1}}}
	for _, c := range orderCases(events, lists) {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				if member, pairs := playOrder(t, c); !member || pairs != 1 {
					t.Fatalf("member = %v, pairs = %d; want inside the room", member, pairs)
				}
			})
		})
	}
}

func TestAnOldMemberAddedReplayedAfterTheRemovalDoesNotBringTheRoomBack(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		_, sink := f.connect("t1", "u", orderRoom)
		sink.take()
		f.route(removed("t1", orderRoom, "u", 2))
		f.route(added("t1", orderRoom, "u", 1))
		f.route(msg("t1", orderRoom))
		if got := sink.take(); !slices.Equal(got, []string{"event member_removed/t1/7/u/v2"}) {
			t.Fatalf("frames = %v, want only the removal", got)
		}
		f.route(added("t1", orderRoom, "u", 3))
		f.route(msg("t1", orderRoom))
		if got := sink.take(); !slices.Equal(got, []string{"event member_added/t1/7/u/v3", "event msg_created/t1/7//v0"}) {
			t.Fatalf("frames after a real rejoin = %v", got)
		}
	})
}

func TestAMemberEventAlreadyInTheLoadedListIsDroppedEvenAfterReady(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.route(withSeq(msg("t1", 99), markSeq))
		_, sink := f.connect("t1", "u")
		sink.take()
		f.route(withSeq(added("t1", orderRoom, "u", 1), 50))
		f.route(msg("t1", orderRoom))
		if got := sink.take(); len(got) != 0 {
			t.Fatalf("a member_added older than the load reached the user or opened the room: %v", got)
		}
	})
}

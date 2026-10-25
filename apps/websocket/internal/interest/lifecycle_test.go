package interest

import (
	"log/slog"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestNewRejectsMissingParts(t *testing.T) {
	quiet := slog.New(slog.DiscardHandler)
	good := Config{Inflight: 1, OpenPending: time.Second, ReloadRate: 1, UserConnectionsMax: 10}
	for name, build := range map[string]func() (*Table, error){
		"zero inflight": func() (*Table, error) {
			return New(Config{OpenPending: time.Second, ReloadRate: 1, UserConnectionsMax: 10}, newFakeLoader(), quiet)
		},
		"zero open pending": func() (*Table, error) {
			return New(Config{Inflight: 1, ReloadRate: 1, UserConnectionsMax: 10}, newFakeLoader(), quiet)
		},
		"zero reload rate": func() (*Table, error) {
			return New(Config{Inflight: 1, OpenPending: time.Second, UserConnectionsMax: 10}, newFakeLoader(), quiet)
		},
		"zero user connections": func() (*Table, error) {
			return New(Config{Inflight: 1, OpenPending: time.Second, ReloadRate: 1}, newFakeLoader(), quiet)
		},
		"reload rate under a nanosecond": func() (*Table, error) {
			return New(Config{Inflight: 1, OpenPending: time.Second, ReloadRate: 2_000_000_000, UserConnectionsMax: 10}, newFakeLoader(), quiet)
		},
		"no loader": func() (*Table, error) { return New(good, nil, quiet) },
		"no logger": func() (*Table, error) { return New(good, newFakeLoader(), nil) },
	} {
		if _, err := build(); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
}

func TestConnectionsOfOneUserShareOneLoad(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		f.loader.holding(true)
		sinks := []*fakeSink{{}, {}, {}}
		for _, s := range sinks {
			mustRegister(t, f.table, "t1", "u", s)
		}
		synctest.Wait()
		if got := f.loader.callsOf("t1", "u"); got != 1 {
			t.Fatalf("loads while three connections wait = %d, want 1", got)
		}
		f.loader.replies <- reply{rooms: []Room{{ID: 1, Ver: 1}}}
		synctest.Wait()
		late := &fakeSink{}
		mustRegister(t, f.table, "t1", "u", late)
		for i, s := range append(sinks, late) {
			if got := s.take(); !slices.Equal(got, []string{"ready"}) {
				t.Fatalf("connection %d frames = %v", i, got)
			}
		}
		if got := f.loader.callsOf("t1", "u"); got != 1 {
			t.Fatalf("loads after a late connection = %d, want 1", got)
		}
		if f.table.stats.users.Load() != 1 || f.table.stats.pairs.Load() != 1 {
			t.Fatalf("users %d pairs %d, want 1 and 1", f.table.stats.users.Load(), f.table.stats.pairs.Load())
		}
	})
}

func TestTheLastConnectionLeavingClearsTheUserAndItsMarks(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixture(t)
		a, _ := f.connect("t1", "u", 1, 2)
		b, _ := f.connect("t1", "u")
		a.Open("1")
		b.Open("9")
		f.route(removed("t1", 3, "u", 5))
		a.Unregister()
		a.Unregister()
		if f.table.stats.users.Load() != 1 || f.table.stats.pairs.Load() != 2 {
			t.Fatalf("after the first leaves: users %d pairs %d", f.table.stats.users.Load(), f.table.stats.pairs.Load())
		}
		b.Unregister()
		time.Sleep(time.Minute)
		synctest.Wait()
		if f.table.stats.users.Load() != 0 || f.table.stats.pairs.Load() != 0 {
			t.Fatalf("after the last leaves: users %d pairs %d", f.table.stats.users.Load(), f.table.stats.pairs.Load())
		}
		for i := range f.table.users {
			if len(f.table.users[i].users) != 0 || len(f.table.rooms[i].members) != 0 || len(f.table.rooms[i].open) != 0 {
				t.Fatalf("shard %d still holds entries", i)
			}
		}
		f.loader.set("t1", "u")
		_, sink := f.connect("t1", "u")
		f.route(added("t1", 3, "u", 1))
		if got := f.loader.callsOf("t1", "u"); got != 2 {
			t.Fatalf("loads after reconnecting = %d, want 2", got)
		}
		if got := sink.take(); !slices.Equal(got, []string{"ready", "event member_added/t1/3/u/v1"}) {
			t.Fatalf("frames after reconnecting = %v; the left mark should be gone with the last connection", got)
		}
	})
}

func TestLoadsInFlightAreBoundedAndALeavingUserFreesItsSlot(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newFixtureWith(t, Config{Inflight: 2, OpenPending: time.Second, ReloadRate: 1, UserConnectionsMax: 10})
		f.loader.holding(true)
		names := []string{"a", "b", "c", "d", "e"}
		conns := map[string]*Conn{}
		for _, u := range names {
			conns[u] = mustRegister(t, f.table, "t1", u, &fakeSink{})
		}
		synctest.Wait()
		if peak, active := f.loader.load(); peak != 2 || active != 2 {
			t.Fatalf("loads in flight peak %d active %d, want 2", peak, active)
		}
		for _, u := range names {
			if f.loader.callsOf("t1", u) == 1 {
				conns[u].Unregister()
				break
			}
		}
		synctest.Wait()
		called := 0
		for _, u := range names {
			called += f.loader.callsOf("t1", u)
		}
		if _, active := f.loader.load(); active != 2 || called != 3 {
			t.Fatalf("a leaving user did not free its slot: active %d, called %d", active, called)
		}
		for range 4 {
			f.loader.replies <- reply{}
		}
		synctest.Wait()
		if peak, _ := f.loader.load(); peak != 2 || f.table.stats.users.Load() != 4 {
			t.Fatalf("peak %d users %d", peak, f.table.stats.users.Load())
		}
	})
}

func TestCloseCancelsLoadsInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loader := newFakeLoader()
		loader.holding(true)
		table, err := New(Config{Inflight: 1, OpenPending: time.Second, ReloadRate: 1, UserConnectionsMax: 10}, loader, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		sink := &fakeSink{}
		c := mustRegister(t, table, "t1", "u", sink)
		mustRegister(t, table, "t1", "v", &fakeSink{})
		synctest.Wait()
		table.Close()
		if sink.failures() != 0 || table.stats.loadFailures.Load() != 0 {
			t.Fatal("a load canceled by Close counted as a failure")
		}
		c.Unregister()
	})
}

func TestRegisterAfterCloseFailsTheConnectionWithoutStartingALoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loader := newFakeLoader()
		table, err := New(Config{Inflight: 1, OpenPending: time.Second, ReloadRate: 1, UserConnectionsMax: 10}, loader, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		table.Close()
		sink := &fakeSink{}
		c := mustRegister(t, table, "t1", "u", sink)
		synctest.Wait()
		c.Open("1")
		c.Close("1")
		c.Unregister()
		if sink.failures() != 1 || loader.callsOf("t1", "u") != 0 || table.stats.users.Load() != 0 || len(sink.take()) != 0 {
			t.Fatalf("failures %d loads %d users %d", sink.failures(), loader.callsOf("t1", "u"), table.stats.users.Load())
		}
	})
}

func TestSourcesNameEveryMetricWithFixedLabels(t *testing.T) {
	f := newFixture(t)
	got := map[string]int{}
	for _, s := range f.table.Sources() {
		var key strings.Builder
		key.WriteString(s.Name)
		for k, v := range s.Labels {
			key.WriteString("{" + k + "=" + v + "}")
		}
		got[key.String()]++
	}
	for _, want := range []string{
		"interest_users", "interest_room_pairs", "room_ids_load_seconds", "room_ids_load_failures_total", "open_room_rejected_total",
		"events_delivered_total{level=member}", "events_delivered_total{level=open}", "events_delivered_total{level=owner}",
		"events_dropped_total{reason=unknown_kind}", "events_dropped_total{reason=malformed}",
		"users_suspended", "reloads_total", "reload_seconds", "reload_failures_total", "event_delivery_seconds",
		"resyncs_total{reason=lag_too_old}", "resyncs_total{reason=consumer_lost}", "resyncs_total{reason=client_backlog}",
	} {
		if got[want] != 1 {
			t.Errorf("metric %s registered %d times", want, got[want])
		}
	}
	if len(got) != 18 {
		t.Fatalf("metrics = %v", got)
	}
}

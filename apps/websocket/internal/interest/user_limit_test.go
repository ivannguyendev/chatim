package interest

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func newLimitedFixture(t *testing.T, max int) *fixture {
	return newFixtureWith(t, Config{Inflight: 4, OpenPending: 10 * time.Second, ReloadRate: 1000, UserConnectionsMax: max})
}

func TestRegisterOverTheUserLimitIsRefusedWithoutALoad(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newLimitedFixture(t, 3)
		for range 3 {
			f.connect("t1", "u", 1)
		}
		loads := f.loader.callsOf("t1", "u")
		extra := &fakeSink{}
		c, err := f.table.Register("t1", "u", extra)
		synctest.Wait()
		if !errors.Is(err, ErrTooManyConnections) || c != nil {
			t.Fatalf("fourth Register = %v, %v; want ErrTooManyConnections", c, err)
		}
		if got := f.loader.callsOf("t1", "u"); got != loads {
			t.Fatalf("loads = %d, want %d", got, loads)
		}
		if got := extra.take(); len(got) != 0 || extra.failures() != 0 {
			t.Fatalf("refused sink got %v and %d failures", got, extra.failures())
		}
		f.route(msg("t1", 1))
		if got := extra.take(); len(got) != 0 {
			t.Fatalf("refused sink got event frames %v", got)
		}
	})
}

func TestUserLimitCountsEachUserApart(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newLimitedFixture(t, 2)
		f.connect("t1", "u")
		f.connect("t1", "u")
		_, other := f.connect("t1", "v")
		_, tenant := f.connect("t2", "u")
		for name, s := range map[string]*fakeSink{"other user": other, "other tenant": tenant} {
			if got := s.take(); !slices.Equal(got, []string{"ready"}) {
				t.Fatalf("%s frames = %v, want ready", name, got)
			}
		}
	})
}

func TestClosingAConnectionFreesAPlaceUnderTheUserLimit(t *testing.T) {
	run(t, func(t *testing.T) {
		f := newLimitedFixture(t, 2)
		first, _ := f.connect("t1", "u")
		f.connect("t1", "u")
		if _, err := f.table.Register("t1", "u", &fakeSink{}); !errors.Is(err, ErrTooManyConnections) {
			t.Fatalf("third Register = %v, want ErrTooManyConnections", err)
		}
		first.Unregister()
		_, late := f.connect("t1", "u")
		if got := late.take(); !slices.Equal(got, []string{"ready"}) {
			t.Fatalf("frames after a place freed = %v, want ready", got)
		}
	})
}

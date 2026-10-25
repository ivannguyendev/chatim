package interest

import (
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
)

type fixture struct {
	t      *testing.T
	table  *Table
	loader *fakeLoader
	seq    uint64
}

func newFixture(t *testing.T) *fixture {
	return newFixtureWith(t, Config{Inflight: 64, OpenPending: 10 * time.Second, ReloadRate: 1000, UserConnectionsMax: 10})
}

func newFixtureWith(t *testing.T, cfg Config) *fixture {
	t.Helper()
	loader := newFakeLoader()
	table, err := New(cfg, loader, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(table.Close)
	return &fixture{t: t, table: table, loader: loader}
}

func (f *fixture) connect(tenant, user string, rooms ...uint64) (*Conn, *fakeSink) {
	f.t.Helper()
	if len(rooms) > 0 {
		f.loader.set(tenant, user, rooms...)
	}
	sink := &fakeSink{}
	c := mustRegister(f.t, f.table, tenant, user, sink)
	synctest.Wait()
	return c, sink
}

func (f *fixture) route(env feed.Envelope) {
	if env.Seq == 0 {
		f.seq++
		env.Seq = f.seq
	}
	f.seq = max(f.seq, env.Seq)
	f.table.Route(env)
}

func mustRegister(t *testing.T, table *Table, tenant, user string, sink Sink) *Conn {
	t.Helper()
	c, err := table.Register(tenant, user, sink)
	if err != nil {
		t.Fatalf("Register %s/%s: %v", tenant, user, err)
	}
	return c
}

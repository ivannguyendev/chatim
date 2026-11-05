package directory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/auth"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var errDown = errors.New("mongodb down")

func entry(tenant, id, name string, ver int64) store.DirectoryEntry {
	return store.DirectoryEntry{TenantID: tenant, ID: id, DisplayName: name, State: store.StateActive, Ver: ver}
}

type fakeRevocations struct {
	mu     sync.Mutex
	snap   auth.RevocationSnapshot
	err    error
	calls  int
	onCall func()
}

func (f *fakeRevocations) Revocations(context.Context) (auth.RevocationSnapshot, error) {
	f.mu.Lock()
	f.calls++
	snap, err, hook := f.snap, f.err, f.onCall
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return snap, err
}

func (f *fakeRevocations) set(snap auth.RevocationSnapshot, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap, f.err = snap, err
}

func (f *fakeRevocations) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func displayName(c *directory.Cache, tenant, id string) string {
	found := c.Lookup(tenant, []string{id})
	if len(found) == 0 {
		return ""
	}
	return found[0].DisplayName
}

func gaps(calls []time.Time) []time.Duration {
	var out []time.Duration
	for i := 1; i < len(calls); i++ {
		out = append(out, calls[i].Sub(calls[i-1]))
	}
	return out
}

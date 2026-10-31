package directory_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var errDown = errors.New("postgres down")

func userChange(tenant, id, name string, ver int64) store.Change {
	return store.Change{Kind: store.ChangeUser, TenantID: tenant, ID: id, Ver: ver,
		User: &store.UserChange{State: store.StateActive, DisplayName: name, Groups: []string{}}}
}

func groupChange(tenant, id, name string, ver int64) store.Change {
	return store.Change{Kind: store.ChangeGroup, TenantID: tenant, ID: id, Ver: ver,
		Group: &store.GroupChange{Name: name, Mentionable: true, State: store.GroupLive}}
}

func entry(tenant, id, name string, ver int64) store.DirectoryEntry {
	return store.DirectoryEntry{TenantID: tenant, ID: id, DisplayName: name, State: store.StateActive, Ver: ver}
}

type fakeReader struct {
	mu            sync.Mutex
	log           []store.Change
	entries       []store.DirectoryEntry
	pending       bool
	changesErr    error
	slotErrs      int
	slotBusy      int
	slotCalls     []time.Time
	releases      int
	loadPageCalls int
	changesCalls  int
	changesTimes  []time.Time
	onLoadPage    func(call int) error
}

func (r *fakeReader) add(changes ...store.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, changes...)
}

func (r *fakeReader) set(apply func(r *fakeReader)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	apply(r)
}

func (r *fakeReader) read(get func(r *fakeReader)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	get(r)
}

func (r *fakeReader) Changes(_ context.Context, after store.Position, limit int) (store.ChangePage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changesCalls++
	r.changesTimes = append(r.changesTimes, time.Now())
	if r.changesErr != nil {
		return store.ChangePage{}, r.changesErr
	}
	from := min(int(after.Xid), len(r.log))
	to := min(from+limit, len(r.log))
	page := store.ChangePage{Changes: slices.Clone(r.log[from:to]), Next: after, PendingAboveHorizon: r.pending}
	if to > from {
		page.Next = store.Position{Xid: uint64(to)}
	}
	return page, nil
}

func (r *fakeReader) LoadStart(context.Context) (store.Position, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return store.Position{Xid: uint64(len(r.log))}, nil
}

func (r *fakeReader) LoadPage(_ context.Context, after store.AccountKey, limit int) ([]store.DirectoryEntry, error) {
	r.mu.Lock()
	r.loadPageCalls++
	call, hook := r.loadPageCalls, r.onLoadPage
	r.mu.Unlock()
	if hook != nil {
		if err := hook(call); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var page []store.DirectoryEntry
	for _, e := range r.entries {
		if cmp.Or(cmp.Compare(e.TenantID, after.TenantID), cmp.Compare(e.ID, after.ID)) > 0 && len(page) < limit {
			page = append(page, e)
		}
	}
	return page, nil
}

func (r *fakeReader) TryLoadSlot(context.Context, int) (func(), bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slotCalls = append(r.slotCalls, time.Now())
	if r.slotErrs > 0 {
		r.slotErrs--
		return nil, false, errDown
	}
	if r.slotBusy > 0 {
		r.slotBusy--
		return nil, false, nil
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.releases++
		})
	}, true, nil
}

type fakeRevocations struct {
	mu     sync.Mutex
	snap   revocation.Snapshot
	err    error
	calls  int
	onCall func()
}

func (f *fakeRevocations) Revocations(context.Context) (revocation.Snapshot, error) {
	f.mu.Lock()
	f.calls++
	snap, err, hook := f.snap, f.err, f.onCall
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return snap, err
}

func (f *fakeRevocations) set(snap revocation.Snapshot, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap, f.err = snap, err
}

func (f *fakeRevocations) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type listenEvent struct {
	notice store.Notice
	err    error
}

type fakeListener struct {
	events chan listenEvent
	mu     sync.Mutex
	closed bool
}

func newFakeListener() *fakeListener { return &fakeListener{events: make(chan listenEvent)} }

func (l *fakeListener) Wait(ctx context.Context) (store.Notice, error) {
	select {
	case <-ctx.Done():
		return store.NoticeNone, ctx.Err()
	case e := <-l.events:
		return e.notice, e.err
	}
}

func (l *fakeListener) Close(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	return nil
}

func (l *fakeListener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

type listenFactory struct {
	mu        sync.Mutex
	listeners []*fakeListener
	failFirst int
	calls     []time.Time
}

func (f *listenFactory) open(context.Context) (store.Listener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, time.Now())
	if f.failFirst > 0 {
		f.failFirst--
		return nil, errDown
	}
	if len(f.listeners) == 0 {
		return nil, errDown
	}
	l := f.listeners[0]
	f.listeners = f.listeners[1:]
	return l, nil
}

func (f *listenFactory) callTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type rig struct {
	reader *fakeReader
	revs   *fakeRevocations
	cache  *directory.Cache
	list   *revocation.List
	stats  *directory.Stats
}

func newRig(t *testing.T) *rig {
	t.Helper()
	list, err := iamsync.NewList(0)
	if err != nil {
		t.Fatalf("NewList: %v", err)
	}
	cache := directory.NewCache()
	return &rig{reader: &fakeReader{}, revs: &fakeRevocations{}, cache: cache, list: list, stats: directory.NewStats(cache)}
}

func (r *rig) loader(onLoaded func()) *directory.Loader {
	return &directory.Loader{Reader: r.reader, Revocations: r.revs, Cache: r.cache, List: r.list, Slots: 4, OnLoaded: onLoaded, Stats: r.stats}
}

type runningFollower struct {
	*directory.Follower
	cancel  context.CancelFunc
	done    chan error
	stopped sync.Once
}

func (r *rig) startFollower(listen *listenFactory) *runningFollower {
	f := &directory.Follower{Listen: listen.open, Reader: r.reader, Revocations: r.revs, Cache: r.cache, List: r.list, Stats: r.stats}
	ctx, cancel := context.WithCancel(context.Background())
	run := &runningFollower{Follower: f, cancel: cancel, done: make(chan error, 1)}
	go func() { run.done <- f.Run(ctx) }()
	return run
}

func (f *runningFollower) stop(t *testing.T) {
	t.Helper()
	f.stopped.Do(func() {
		f.cancel()
		if err := <-f.done; !errors.Is(err, context.Canceled) {
			t.Errorf("Follower.Run = %v; want context.Canceled", err)
		}
	})
}

func displayName(c *directory.Cache, tenant, id string) string {
	found := c.Lookup(tenant, []string{id})
	if len(found) == 0 {
		return ""
	}
	return found[0].DisplayName
}

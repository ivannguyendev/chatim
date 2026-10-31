package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const feedPath = "/internal/v1/directory/changes"

type fakeFeed struct {
	mu        sync.Mutex
	changes   []store.Change
	next      store.Position
	err       error
	afters    []store.Position
	limits    []int
	deadlines []time.Time
}

func (f *fakeFeed) Changes(ctx context.Context, after store.Position, limit int) (store.ChangePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	deadline, _ := ctx.Deadline()
	f.deadlines = append(f.deadlines, deadline)
	f.afters = append(f.afters, after)
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return store.ChangePage{}, f.err
	}
	if len(f.changes) == 0 {
		return store.ChangePage{Next: after}, nil
	}
	page := store.ChangePage{Changes: f.changes, Next: f.next}
	f.changes = nil
	return page, nil
}

func (f *fakeFeed) push(next store.Position, changes ...store.Change) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changes, f.next = changes, next
}

func (f *fakeFeed) queryDeadlines() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deadlines)
}

func (f *fakeFeed) calls() ([]store.Position, []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.afters), slices.Clone(f.limits)
}

type fakeSignal struct {
	mu sync.Mutex
	ch chan struct{}
}

func (s *fakeSignal) Signal() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}

func (s *fakeSignal) fire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch != nil {
		close(s.ch)
	}
	s.ch = make(chan struct{})
}

type feedHarness struct {
	feed    *fakeFeed
	signal  *fakeSignal
	stats   *api.FeedStats
	handler *api.Internal
}

func newFeedHarness(t *testing.T, waitMax time.Duration, adjust ...func(*api.InternalOptions)) *feedHarness {
	t.Helper()
	f := &feedHarness{feed: &fakeFeed{}, signal: &fakeSignal{}, stats: &api.FeedStats{}}
	opts := api.InternalOptions{
		Revocations: &fakeRevocations{}, Secret: nodeSecret,
		DirectoryFeed: f.feed, DirectorySignal: f.signal, FeedWaitMax: waitMax, FeedStats: f.stats,
	}
	for _, a := range adjust {
		a(&opts)
	}
	h, err := api.NewInternal(opts)
	if err != nil {
		t.Fatalf("NewInternal: %v", err)
	}
	f.handler = h
	return f
}

func (f *feedHarness) serve(query string, secret string) *httptest.ResponseRecorder {
	return f.serveMethod(http.MethodGet, query, secret)
}

func (f *feedHarness) serveMethod(method, query, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, feedPath+query, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *feedHarness) start(query string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.serve(query, nodeSecret) }()
	return done
}

type feedBody struct {
	Changes []map[string]any `json:"changes"`
	Next    string           `json:"next"`
}

func decodeFeed(t *testing.T, rec *httptest.ResponseRecorder) feedBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s; want 200", rec.Code, rec.Body)
	}
	var body feedBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Changes == nil {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
	return body
}

func requireNotDone(t *testing.T, done <-chan *httptest.ResponseRecorder) {
	t.Helper()
	select {
	case rec := <-done:
		t.Fatalf("answered while waiting: %d %s", rec.Code, rec.Body)
	default:
	}
}

var (
	feedStart = store.Position{Xid: 7, Kind: store.ChangeUser, Tenant: "shop", ID: "lan"}
	feedNext  = store.Position{Xid: 9, Kind: store.ChangeGroup, Tenant: "shop", ID: "sales"}
)

func userChange(id string, groups []string) store.Change {
	return store.Change{Kind: store.ChangeUser, TenantID: "shop", ID: id, Ver: 3,
		User: &store.UserChange{State: store.StateActive, DisplayName: "Lan", AvatarURL: "https://cdn/lan.png", Groups: groups}}
}

func groupChange() store.Change {
	return store.Change{Kind: store.ChangeGroup, TenantID: "shop", ID: "sales", Ver: 4,
		Group: &store.GroupChange{Name: "Sales", Mentionable: true, State: store.GroupDeleted}}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

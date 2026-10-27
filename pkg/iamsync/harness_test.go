package iamsync_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/pkg/iamsync"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	internalURL = "http://chatim-iam-1:8081"
	jwksURL     = "https://iam.example.com/.well-known/jwks.json"
	secret      = "node-secret-value-0123456789abcdef"
)

type answer struct {
	status int
	body   []byte
	etag   string
	err    error
}

type fakeDoer struct {
	mu        sync.Mutex
	revoke    answer
	keys      answer
	revCalls  []*http.Request
	keyCalls  int
	keyCalled chan struct{}
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	var a answer
	if req.URL.String() == jwksURL {
		f.keyCalls++
		a = f.keys
		if f.keyCalled != nil {
			select {
			case f.keyCalled <- struct{}{}:
			default:
			}
		}
	} else {
		f.revCalls = append(f.revCalls, req)
		a = f.revoke
	}
	f.mu.Unlock()
	if a.err != nil {
		return nil, a.err
	}
	h := http.Header{}
	if a.etag != "" {
		h.Set("ETag", a.etag)
	}
	return &http.Response{StatusCode: a.status, Header: h, Body: io.NopCloser(bytes.NewReader(a.body))}, nil
}

func (f *fakeDoer) setRevoke(a answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoke = a
}

func (f *fakeDoer) keyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keyCalls
}

func (f *fakeDoer) lastRevokeRequest() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revCalls[len(f.revCalls)-1]
}

type fakeKeys struct {
	mu   sync.Mutex
	sets [][]byte
}

func (f *fakeKeys) SetDynamic(jwks []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, jwks)
	return 0, nil
}

func (f *fakeKeys) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sets)
}

type fakeShared struct {
	mu    sync.Mutex
	value []byte
	ttl   time.Duration
	puts  int
	err   error
}

func (f *fakeShared) Get(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value, f.err
}

func (f *fakeShared) Put(_ context.Context, v []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.value, f.ttl = v, ttl
	f.puts++
	return nil
}

func (f *fakeShared) snapshot() (value []byte, ttl time.Duration, puts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value, f.ttl, f.puts
}

func snapshotBody(t *testing.T, s revocation.Snapshot) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sharedValue(t *testing.T, fetched time.Time, s revocation.Snapshot) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"fetched_at": fetched, "snapshot": json.RawMessage(snapshotBody(t, s))})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func phoneRevoked(expires time.Time) revocation.Snapshot {
	return revocation.Snapshot{Sessions: []revocation.Session{{ID: "iphone", Tenant: "shop", ExpiresAt: expires}}}
}

type rig struct {
	doer    *fakeDoer
	keys    *fakeKeys
	shared  *fakeShared
	list    *revocation.List
	syncer  *iamsync.Syncer
	added   chan revocation.Added
	cancel  context.CancelFunc
	stopped chan struct{}
}

func testConfig() iamsync.Config {
	return iamsync.Config{
		InternalURL: internalURL, JWKSURL: jwksURL, Secret: secret,
		RevocationsEvery: 2 * time.Second, KeysEvery: time.Minute, KeysMinGap: 5 * time.Second,
		ReadyTimeout: 10 * time.Second, HTTPTimeout: 2 * time.Second, BodyMax: 1 << 20, SharedTTL: time.Minute,
	}
}

func newRig(t *testing.T, cfg iamsync.Config, doer *fakeDoer, shared *fakeShared) *rig {
	t.Helper()
	list, err := revocation.New(revocation.Config{Leeway: 30 * time.Second, MaxKeep: 17 * time.Minute, MaxAhead: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{doer: doer, keys: &fakeKeys{}, shared: shared, list: list, added: make(chan revocation.Added, 16), stopped: make(chan struct{})}
	var sh iamsync.Shared
	if shared != nil {
		sh = shared
	}
	r.syncer, err = iamsync.New(cfg, list, r.keys, sh, doer, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.syncer.OnAdded(func(a revocation.Added) { r.added <- a })
	return r
}

func (r *rig) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() {
		defer close(r.stopped)
		r.syncer.Run(ctx)
	}()
}

func (r *rig) stop() {
	r.cancel()
	<-r.stopped
}

func okKeys() answer {
	return answer{status: http.StatusOK, body: []byte(`{"keys":[]}`)}
}

var errDown = errors.New("connection refused")

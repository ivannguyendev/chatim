package api_test

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/identity/v1/identityv1connect"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

const (
	tokenLan     = "tok-lan"
	tokenOld     = "tok-old"
	tokenRevoked = "tok-revoked"
	tokenMall    = "tok-mall"
	tokenGhost   = "tok-ghost"
)

type fakeClaims map[string]jwtverify.Claims

func (f fakeClaims) Claims(token string) (jwtverify.Claims, error) {
	c, ok := f[token]
	if !ok {
		return jwtverify.Claims{}, errors.New("bad token")
	}
	return c, nil
}

var testClaims = fakeClaims{
	tokenLan:     {Tenant: "shop", User: "lan", Session: "s-lan", IssuedAt: t0.Add(time.Second)},
	tokenOld:     {Tenant: "shop", User: "lan", Session: "s-old", IssuedAt: t0.Add(-time.Minute)},
	tokenRevoked: {Tenant: "shop", User: "lan", Session: "s-revoked", IssuedAt: t0.Add(time.Second)},
	tokenMall:    {Tenant: "mall", User: "zed", Session: "s-mall", IssuedAt: t0.Add(time.Second)},
	tokenGhost:   {Tenant: "shop", User: "ghost", Session: "s-ghost", IssuedAt: t0.Add(time.Second)},
}

type testDirectory struct {
	*directory.Cache
	ready   atomic.Bool
	lookups atomic.Int64
}

func (d *testDirectory) Ready() bool { return d.ready.Load() }

func (d *testDirectory) Lookup(tenant string, ids []string) []directory.User {
	d.lookups.Add(1)
	return d.Cache.Lookup(tenant, ids)
}

func directoryEntry(tenant, id, name string, state store.State) store.DirectoryEntry {
	return store.DirectoryEntry{TenantID: tenant, ID: id, DisplayName: name, State: state, Ver: 1}
}

type dirHarness struct {
	*harness
	dir    *testDirectory
	list   *revocation.List
	gate   *userlimit.Gate
	client identityv1connect.DirectoryServiceClient
}

func newDirHarness(t *testing.T, mods ...func(*api.Options)) *dirHarness {
	t.Helper()
	list := newTestRevocations(t)
	gate, err := userlimit.NewGate(8)
	if err != nil {
		t.Fatal(err)
	}
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: 1000, Burst: 1000, Inflight: 8, Capacity: 100, Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	d := &dirHarness{dir: &testDirectory{Cache: directory.NewCache()}, list: list, gate: gate}
	d.dir.ready.Store(true)
	d.dir.ApplyEntries([]store.DirectoryEntry{
		directoryEntry("shop", "lan", "lan", store.StateActive),
		directoryEntry("shop", "minh", "Minh", store.StateDisabled),
		directoryEntry("mall", "zed", "Zed", store.StateActive),
		directoryEntry("mall", "lan", "Mall Lan", store.StateActive),
	})
	d.dir.ApplyEntries([]store.DirectoryEntry{{TenantID: "shop", ID: "hoa", DisplayName: "Hoa", AvatarURL: "https://cdn.example/hoa.png", State: store.StateActive, Ver: 1}})
	wire := func(o *api.Options) {
		o.Directory, o.Revocations, o.Claims, o.DirectoryGate, o.DirectoryUsers = d.dir, list, testClaims, gate, users
	}
	d.harness = newHarness(t, append([]func(*api.Options){wire}, mods...)...)
	d.client = identityv1connect.NewDirectoryServiceClient(http.DefaultClient, d.server.URL)
	return d
}

func newTestRevocations(t *testing.T) *revocation.List {
	t.Helper()
	list, err := revocation.New(revocation.Config{MaxKeep: time.Hour, MaxAhead: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	list.Merge(revocation.Snapshot{Sessions: []revocation.Session{{ID: "s-revoked", Tenant: "shop", ExpiresAt: t0.Add(15 * time.Minute)}}}, t0)
	return list
}

func withToken[T any](token string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+token)
	return req
}

type refusal struct {
	message    string
	retryAfter string
}

func wantCode(t *testing.T, err error, code connect.Code, step string) refusal {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != code {
		t.Fatalf("%s: err = %v; want %v", step, err, code)
	}
	return refusal{message: ce.Message(), retryAfter: ce.Meta().Get("Retry-After")}
}

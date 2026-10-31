package api_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

const (
	tokenLan     = "tok-lan"
	tokenOld     = "tok-old"
	tokenRevoked = "tok-revoked"
	tokenMall    = "tok-mall"
)

type profileFake struct {
	calls   int
	profile store.Profile
	updates []store.ProfileUpdate
}

func (f *fakeAuth) MyProfile(_ context.Context, c auth.Caller) (store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles.calls++
	f.callers = append(f.callers, c)
	if f.err != nil {
		return store.Profile{}, f.err
	}
	return f.profiles.profile, nil
}

func (f *fakeAuth) UpdateMyProfile(_ context.Context, c auth.Caller, u store.ProfileUpdate) (store.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles.calls++
	f.callers = append(f.callers, c)
	f.profiles.updates = append(f.profiles.updates, u)
	if f.err != nil {
		return store.Profile{}, f.err
	}
	if u.DisplayName != nil {
		f.profiles.profile.DisplayName = *u.DisplayName
	}
	if u.AvatarURL != nil {
		f.profiles.profile.AvatarURL = *u.AvatarURL
	}
	return f.profiles.profile, nil
}

func (f *fakeAuth) profileCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.profiles.calls
}

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
	client iamv1connect.DirectoryServiceClient
}

func newDirHarness(t *testing.T, mods ...func(*api.Options)) *dirHarness {
	t.Helper()
	list, err := revocation.New(revocation.Config{MaxKeep: time.Hour, MaxAhead: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	list.Merge(revocation.Snapshot{Sessions: []revocation.Session{{ID: "s-revoked", Tenant: "shop", ExpiresAt: t0.Add(15 * time.Minute)}}}, t0)
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
	wire := func(o *api.Options) {
		o.Directory, o.Revocations, o.Claims, o.DirectoryGate, o.DirectoryUsers = d.dir, list, testClaims, gate, users
	}
	d.harness = newHarness(t, append([]func(*api.Options){wire}, mods...)...)
	d.fake.profiles.profile = store.Profile{ID: "lan", Username: "lan", Email: "lan@example.com", DisplayName: "lan"}
	d.client = iamv1connect.NewDirectoryServiceClient(http.DefaultClient, d.server.URL)
	return d
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

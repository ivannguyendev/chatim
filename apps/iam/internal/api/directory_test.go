package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

type directoryCall func(d *dirHarness, token string) error

var directoryCalls = map[string]directoryCall{
	"GetUsers": func(d *dirHarness, token string) error {
		_, err := d.client.GetUsers(context.Background(), withToken(token, &iamv1.GetUsersRequest{UserIds: []string{"lan"}}))
		return err
	},
	"GetMyProfile": func(d *dirHarness, token string) error {
		_, err := d.client.GetMyProfile(context.Background(), withToken(token, &iamv1.GetMyProfileRequest{}))
		return err
	},
	"UpdateMyProfile": func(d *dirHarness, token string) error {
		_, err := d.client.UpdateMyProfile(context.Background(), withToken(token, &iamv1.UpdateMyProfileRequest{DisplayName: new("Lan")}))
		return err
	},
}

func getUsers(d *dirHarness, token string, ids ...string) (*iamv1.GetUsersResponse, error) {
	resp, err := d.client.GetUsers(context.Background(), withToken(token, &iamv1.GetUsersRequest{UserIds: ids}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func usersOf(resp *iamv1.GetUsersResponse) string {
	var parts []string
	for _, u := range resp.GetUsers() {
		parts = append(parts, fmt.Sprintf("%s=%s/%s", u.GetId(), u.GetDisplayName(), u.GetState()))
	}
	return strings.Join(parts, ",")
}

func TestGetUsersReturnsOnlyTheCallersTenant(t *testing.T) {
	d := newDirHarness(t)
	resp, err := getUsers(d, tokenLan, "lan", "minh", "zed", "lan", "ghost")
	if err != nil {
		t.Fatalf("GetUsers: %v", err)
	}
	if got, want := usersOf(resp), "lan=lan/USER_STATE_ACTIVE,minh=Minh/USER_STATE_DISABLED"; got != want {
		t.Fatalf("users = %s; want %s", got, want)
	}
	resp, err = getUsers(d, tokenMall, "lan", "minh")
	if err != nil || usersOf(resp) != "lan=Mall Lan/USER_STATE_ACTIVE" {
		t.Fatalf("mall users = %v, %v", resp, err)
	}
}

func TestGetUsersNeverReturnsEmailOrUsername(t *testing.T) {
	fields := (&iamv1.DirectoryUser{}).ProtoReflect().Descriptor().Fields()
	for i := range fields.Len() {
		if name := fields.Get(i).Name(); name == "email" || name == "username" {
			t.Fatalf("DirectoryUser has a field %q", name)
		}
	}
}

func TestRevokedSessionIsRefusedWithoutReadingTheStore(t *testing.T) {
	d := newDirHarness(t)
	for name, call := range directoryCalls {
		ce := wantCode(t, call(d, tokenRevoked), connect.CodeUnauthenticated, name)
		if ce.message != "session revoked" {
			t.Fatalf("%s: message = %q", name, ce.message)
		}
		wantCode(t, call(d, "forged"), connect.CodeUnauthenticated, name+" forged")
	}
	if d.fake.profileCalls() != 0 || d.dir.lookups.Load() != 0 {
		t.Fatalf("store calls = %d, lookups = %d; want 0", d.fake.profileCalls(), d.dir.lookups.Load())
	}
	if d.gate.InFlight() != 0 {
		t.Fatalf("gate in flight = %d after refusals", d.gate.InFlight())
	}
}

func revokeTenantAt(d *dirHarness, at time.Time) {
	d.list.Merge(revocation.Snapshot{Tenants: []revocation.Tenant{{ID: "shop", RevokedBefore: at, ExpiresAt: at.Add(15 * time.Minute)}}}, at)
}

func TestTokenIssuedAfterTenantRevocationIsAllowed(t *testing.T) {
	d := newDirHarness(t)
	revokeTenantAt(d, t0)
	for name, call := range directoryCalls {
		if err := call(d, tokenLan); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestTokenIssuedBeforeTenantRevocationIsRefused(t *testing.T) {
	d := newDirHarness(t)
	if err := directoryCalls["GetUsers"](d, tokenOld); err != nil {
		t.Fatalf("before the revocation: %v", err)
	}
	revokeTenantAt(d, t0)
	for name, call := range directoryCalls {
		wantCode(t, call(d, tokenOld), connect.CodeUnauthenticated, name)
	}
	if d.fake.profileCalls() != 0 {
		t.Fatalf("store calls = %d; want 0", d.fake.profileCalls())
	}
}

func manyIDs(n, size int) []string {
	out := make([]string, n)
	for i := range out {
		id := fmt.Sprintf("u%d-", i)
		out[i] = id + strings.Repeat("x", size-len(id))
	}
	return out
}

func TestMoreThan500IdsIsInvalid(t *testing.T) {
	d := newDirHarness(t)
	if _, err := getUsers(d, tokenLan, manyIDs(501, 8)...); err == nil {
		t.Fatal("501 ids accepted")
	} else {
		wantCode(t, err, connect.CodeInvalidArgument, "501 ids")
	}
	if _, err := getUsers(d, tokenLan); err == nil {
		t.Fatal("0 ids accepted")
	} else {
		wantCode(t, err, connect.CodeInvalidArgument, "0 ids")
	}
	if _, err := getUsers(d, tokenLan, manyIDs(500, 8)...); err != nil {
		t.Fatalf("500 ids: %v", err)
	}
}

func TestGetUsersRejectsBadIdsWithoutEchoingThem(t *testing.T) {
	d := newDirHarness(t)
	for _, bad := range []string{"has space", strings.Repeat("a", 65), "", "tab\tid"} {
		_, err := getUsers(d, tokenLan, "lan", bad)
		ce := wantCode(t, err, connect.CodeInvalidArgument, fmt.Sprintf("id %q", bad))
		if bad != "" && strings.Contains(ce.message, bad) {
			t.Fatalf("message %q echoes the id", ce.message)
		}
	}
	if d.dir.lookups.Load() != 0 {
		t.Fatal("bad ids reached the directory")
	}
}

func TestBodyOver64KiBIsResourceExhausted(t *testing.T) {
	d := newDirHarness(t)
	if _, err := getUsers(d, tokenLan, manyIDs(500, 60)...); err != nil {
		t.Fatalf("a 30 KiB directory body above the 16 KiB auth limit: %v", err)
	}
	big := &iamv1.UpdateMyProfileRequest{DisplayName: new(strings.Repeat("n", 70<<10))}
	_, err := d.client.UpdateMyProfile(context.Background(), withToken(tokenLan, big))
	wantCode(t, err, connect.CodeResourceExhausted, "70 KiB body")
	if d.fake.profileCalls() != 0 {
		t.Fatal("an oversized body reached the service")
	}
}

func TestOAuthBodyLimitIsUnchanged(t *testing.T) {
	d := newDirHarness(t)
	resp := d.postForm(t, tokenPath, refreshBody(strings.Repeat("x", 20<<10)), map[string]string{"DPoP": "good-proof"})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized token body = %d; want 413", resp.StatusCode)
	}
}

func TestNotReadyIsUnavailable(t *testing.T) {
	d := newDirHarness(t)
	d.dir.ready.Store(false)
	_, err := getUsers(d, tokenLan, "lan")
	ce := wantCode(t, err, connect.CodeUnavailable, "not ready")
	if ce.retryAfter != "1" {
		t.Fatalf("Retry-After = %q", ce.retryAfter)
	}
	if d.dir.lookups.Load() != 0 {
		t.Fatal("lookup ran before the directory was ready")
	}
}

func TestUserRateLimitIsResourceExhausted(t *testing.T) {
	for name, call := range directoryCalls {
		t.Run(name, func(t *testing.T) {
			users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: 1, Burst: 1, Inflight: 4, Capacity: 10, Now: func() time.Time { return t0 }})
			if err != nil {
				t.Fatal(err)
			}
			d := newDirHarness(t, func(o *api.Options) { o.DirectoryUsers = users })
			if err := call(d, tokenLan); err != nil {
				t.Fatalf("first call: %v", err)
			}
			wantCode(t, call(d, tokenLan), connect.CodeResourceExhausted, "second call")
			if err := call(d, tokenMall); err != nil {
				t.Fatalf("another user: %v", err)
			}
		})
	}
}

func TestGateFullIsUnavailable(t *testing.T) {
	gate, err := userlimit.NewGate(1)
	if err != nil {
		t.Fatal(err)
	}
	d := newDirHarness(t, func(o *api.Options) { o.DirectoryGate = gate })
	gate.Enter()
	for name, call := range directoryCalls {
		ce := wantCode(t, call(d, tokenLan), connect.CodeUnavailable, name)
		if ce.retryAfter != "1" {
			t.Fatalf("%s: Retry-After = %q", name, ce.retryAfter)
		}
	}
	gate.Leave()
	for name, call := range directoryCalls {
		if err := call(d, tokenLan); err != nil {
			t.Fatalf("%s after the gate freed: %v", name, err)
		}
	}
	if gate.InFlight() != 0 {
		t.Fatalf("gate in flight = %d; want 0", gate.InFlight())
	}
}

func TestUpdateMyProfileRejectsBadInput(t *testing.T) {
	d := newDirHarness(t)
	for _, bad := range []error{directory.ErrDisplayName, directory.ErrAvatarURL, fmt.Errorf("%w: set a display name or an avatar url", apperr.ErrInvalidArgument)} {
		d.fake.err = bad
		_, err := d.client.UpdateMyProfile(context.Background(), withToken(tokenLan, &iamv1.UpdateMyProfileRequest{AvatarUrl: new("http://x")}))
		ce := wantCode(t, err, connect.CodeInvalidArgument, bad.Error())
		if want := strings.TrimPrefix(bad.Error(), apperr.ErrInvalidArgument.Error()+": "); ce.message != want {
			t.Fatalf("message = %q; want %q", ce.message, want)
		}
	}
	d.fake.err = nil
	_, err := d.client.UpdateMyProfile(context.Background(), withToken(tokenLan, &iamv1.UpdateMyProfileRequest{}))
	if err != nil {
		t.Fatalf("empty update with the fake: %v", err)
	}
	if u := d.fake.profiles.updates[len(d.fake.profiles.updates)-1]; u.DisplayName != nil || u.AvatarURL != nil {
		t.Fatalf("unset fields reached the service as set: %+v", u)
	}
}

func TestGetMyProfileReturnsTheCallersProfile(t *testing.T) {
	d := newDirHarness(t)
	req := withToken(tokenLan, &iamv1.GetMyProfileRequest{})
	req.Header().Set("X-Real-IP", "198.51.100.7")
	resp, err := d.client.GetMyProfile(context.Background(), req)
	if err != nil {
		t.Fatalf("GetMyProfile: %v", err)
	}
	p := resp.Msg.GetProfile()
	if p.GetId() != "lan" || p.GetUsername() != "lan" || p.GetEmail() != "lan@example.com" || p.GetDisplayName() != "lan" {
		t.Fatalf("profile = %+v", p)
	}
	if c := d.fake.callers[0]; c.Tenant != "shop" || c.Account != "lan" || c.Session != "s-lan" || c.IP != "198.51.100.7" {
		t.Fatalf("caller = %+v", c)
	}
	d.fake.err = apperr.ErrUnavailable
	_, err = d.client.GetMyProfile(context.Background(), withToken(tokenLan, &iamv1.GetMyProfileRequest{}))
	wantCode(t, err, connect.CodeUnavailable, "store down")
}

func TestUpdateThenGetUsersSeesTheNewName(t *testing.T) {
	d := newDirHarness(t)
	resp, err := d.client.UpdateMyProfile(context.Background(), withToken(tokenLan, &iamv1.UpdateMyProfileRequest{DisplayName: new("Lan Nguyen")}))
	if err != nil {
		t.Fatalf("UpdateMyProfile: %v", err)
	}
	before, err := getUsers(d, tokenLan, "lan")
	if err != nil || usersOf(before) != "lan=lan/USER_STATE_ACTIVE" {
		t.Fatalf("before the change reaches the cache = %v, %v", before, err)
	}
	p := resp.Msg.GetProfile()
	d.dir.Apply([]store.Change{{
		Kind: store.ChangeUser, TenantID: "shop", ID: p.GetId(), Ver: 2,
		User: &store.UserChange{State: store.StateActive, DisplayName: p.GetDisplayName(), AvatarURL: p.GetAvatarUrl()},
	}})
	after, err := getUsers(d, tokenLan, "lan")
	if err != nil || usersOf(after) != "lan=Lan Nguyen/USER_STATE_ACTIVE" {
		t.Fatalf("after the change = %v, %v", after, err)
	}
}

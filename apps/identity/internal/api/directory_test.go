package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/identity/internal/api"
	identityv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/identity/v1"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

type directoryCall func(d *dirHarness, token string) error

var directoryCalls = map[string]directoryCall{
	"GetUsers": func(d *dirHarness, token string) error {
		_, err := d.client.GetUsers(context.Background(), withToken(token, &identityv1.GetUsersRequest{UserIds: []string{"lan"}}))
		return err
	},
	"GetMyProfile": func(d *dirHarness, token string) error {
		_, err := d.client.GetMyProfile(context.Background(), withToken(token, &identityv1.GetMyProfileRequest{}))
		return err
	},
}

func getUsers(d *dirHarness, token string, ids ...string) (*identityv1.GetUsersResponse, error) {
	resp, err := d.client.GetUsers(context.Background(), withToken(token, &identityv1.GetUsersRequest{UserIds: ids}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func usersOf(resp *identityv1.GetUsersResponse) string {
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
	fields := (&identityv1.DirectoryUser{}).ProtoReflect().Descriptor().Fields()
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
	if d.dir.lookups.Load() != 0 {
		t.Fatalf("lookups = %d; want 0", d.dir.lookups.Load())
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
	before := d.dir.lookups.Load()
	revokeTenantAt(d, t0)
	for name, call := range directoryCalls {
		wantCode(t, call(d, tokenOld), connect.CodeUnauthenticated, name)
	}
	if got := d.dir.lookups.Load(); got != before {
		t.Fatalf("lookups = %d after refusals; want %d", got, before)
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
	before := d.dir.lookups.Load()
	_, err := getUsers(d, tokenLan, strings.Repeat("n", 70<<10))
	wantCode(t, err, connect.CodeResourceExhausted, "70 KiB body")
	if d.dir.lookups.Load() != before {
		t.Fatal("an oversized body reached the directory")
	}
}

func TestRemovedLoginAndAdminRoutesAreNotFound(t *testing.T) {
	d := newDirHarness(t)
	for _, path := range []string{
		"/realms/shop/protocol/openid-connect/token",
		"/realms/shop/protocol/openid-connect/revoke",
		"/chatim.identity.v1.AuthService/Login",
		"/chatim.identity.v1.AdminService/CreateRole",
		"/chatim.identity.v1.DirectoryService/UpdateMyProfile",
	} {
		r := d.do(t, http.MethodPost, path, "{}", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + tokenLan})
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("POST %s = %d; want 404", path, r.StatusCode)
		}
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

func TestGetMyProfileReadsTheCallerFromTheDirectory(t *testing.T) {
	d := newDirHarness(t)
	for _, tc := range []struct{ token, id, name string }{{tokenLan, "lan", "lan"}, {tokenMall, "zed", "Zed"}} {
		resp, err := d.client.GetMyProfile(context.Background(), withToken(tc.token, &identityv1.GetMyProfileRequest{}))
		if err != nil {
			t.Fatalf("GetMyProfile %s: %v", tc.id, err)
		}
		if p := resp.Msg.GetProfile(); p.GetId() != tc.id || p.GetDisplayName() != tc.name || p.GetAvatarUrl() != "" {
			t.Fatalf("profile = %+v; want %s/%s", p, tc.id, tc.name)
		}
	}
	r := d.do(t, http.MethodPost, "/chatim.identity.v1.DirectoryService/GetMyProfile", "{}", map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + tokenLan})
	if r.StatusCode != http.StatusOK || !strings.Contains(string(r.Body), `"displayName":"lan"`) {
		t.Fatalf("json reply = %d %s", r.StatusCode, r.Body)
	}
	for _, field := range []string{"username", "email"} {
		if strings.Contains(strings.ToLower(string(r.Body)), field) {
			t.Fatalf("json reply %s carries %s", r.Body, field)
		}
	}
}

func TestGetMyProfileCarriesTheAvatar(t *testing.T) {
	claims := testClaims[tokenLan]
	claims.User = "hoa"
	d := newDirHarness(t, func(o *api.Options) { o.Claims = fakeClaims{"tok-hoa": claims} })
	resp, err := d.client.GetMyProfile(context.Background(), withToken("tok-hoa", &identityv1.GetMyProfileRequest{}))
	if err != nil {
		t.Fatalf("GetMyProfile: %v", err)
	}
	if p := resp.Msg.GetProfile(); p.GetId() != "hoa" || p.GetDisplayName() != "Hoa" || p.GetAvatarUrl() != "https://cdn.example/hoa.png" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestGetMyProfileOfAnUnknownUserIsNotFound(t *testing.T) {
	d := newDirHarness(t)
	_, err := d.client.GetMyProfile(context.Background(), withToken(tokenGhost, &identityv1.GetMyProfileRequest{}))
	wantCode(t, err, connect.CodeNotFound, "unknown user")
}

func TestGetMyProfileIsUnavailableWhileTheDirectoryLoads(t *testing.T) {
	d := newDirHarness(t)
	d.dir.ready.Store(false)
	_, err := d.client.GetMyProfile(context.Background(), withToken(tokenLan, &identityv1.GetMyProfileRequest{}))
	if ce := wantCode(t, err, connect.CodeUnavailable, "not ready"); ce.retryAfter != "1" {
		t.Fatalf("Retry-After = %q", ce.retryAfter)
	}
	if d.dir.lookups.Load() != 0 {
		t.Fatal("lookup ran before the directory was ready")
	}
}

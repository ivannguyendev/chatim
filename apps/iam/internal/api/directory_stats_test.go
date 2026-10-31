package api_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

func TestDirectoryStatsCountGetUsersByResult(t *testing.T) {
	stats := &api.DirectoryStats{}
	d := newDirHarness(t, func(o *api.Options) { o.DirectoryStats = stats })
	if _, err := getUsers(d, tokenLan, "lan", "minh", "lan"); err != nil {
		t.Fatalf("GetUsers: %v", err)
	}
	_, err := getUsers(d, tokenLan, "Bad Id")
	wantCode(t, err, connect.CodeInvalidArgument, "bad id")
	_, err = getUsers(d, "tok-unknown", "lan")
	wantCode(t, err, connect.CodeUnauthenticated, "bad token")
	d.dir.ready.Store(false)
	_, err = getUsers(d, tokenLan, "lan")
	wantCode(t, err, connect.CodeUnavailable, "not ready")
	got := map[string]uint64{
		"ok": stats.GetUsersOK.Load(), "bad_request": stats.GetUsersBadRequest.Load(), "unauthenticated": stats.GetUsersUnauthenticated.Load(),
		"unavailable": stats.GetUsersUnavailable.Load(), "limited": stats.GetUsersLimited.Load(), "error": stats.GetUsersErrors.Load(),
	}
	want := map[string]uint64{"ok": 1, "bad_request": 1, "unauthenticated": 1, "unavailable": 1, "limited": 0, "error": 0}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("get_users %s = %d; want %d (all %v)", k, got[k], v, got)
		}
	}
	if ids := stats.GetUsersIDs.Load(); ids != 2 {
		t.Fatalf("ids = %d; want 2, the unique ids looked up", ids)
	}
}

func TestDirectoryStatsCountUserLimitsAndGateRejections(t *testing.T) {
	stats := &api.DirectoryStats{}
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: 1, Burst: 1, Inflight: 4, Capacity: 10, Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := userlimit.NewGate(1)
	if err != nil {
		t.Fatal(err)
	}
	d := newDirHarness(t, func(o *api.Options) { o.DirectoryStats, o.DirectoryUsers, o.DirectoryGate = stats, users, gate })
	if _, err := getUsers(d, tokenLan, "lan"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err = getUsers(d, tokenLan, "lan")
	wantCode(t, err, connect.CodeResourceExhausted, "over the user rate")
	gate.Enter()
	defer gate.Leave()
	_, err = getUsers(d, tokenMall, "zed")
	wantCode(t, err, connect.CodeUnavailable, "gate full")
	if stats.UserLimited.Load() != 1 || stats.InflightRejected.Load() != 1 || stats.GetUsersLimited.Load() != 1 || stats.GetUsersUnavailable.Load() != 1 {
		t.Fatalf("user limited %d inflight rejected %d get_users limited %d unavailable %d; want 1 each",
			stats.UserLimited.Load(), stats.InflightRejected.Load(), stats.GetUsersLimited.Load(), stats.GetUsersUnavailable.Load())
	}
}

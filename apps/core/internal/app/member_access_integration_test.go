package app

import (
	"context"
	"maps"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itMemberCacheTTL = 10 * time.Second
	itForgetsSeries  = "chatim_core_member_cache_forgets_total"
)

func assertDeniedEverywhere(t *testing.T, client chatimv1.CoreServiceClient, user, roomID string) {
	t.Helper()
	ctx := callerAs(t.Context(), user)
	_, sendErr := retryingUnavailable(ctx, func(ctx context.Context) (*chatimv1.SendMessageResponse, error) {
		return client.SendMessage(ctx, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "denied-" + user, Text: "after removal"})
	})
	history := &chatimv1.GetHistoryRequest{RoomId: roomID, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 10}
	_, historyErr := client.GetHistory(ctx, history)
	_, readErr := client.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: roomID, Seq: 1})
	for call, err := range map[string]error{"SendMessage": sendErr, "GetHistory": historyErr, "MarkRead": readErr} {
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s by removed %s = %v, want PermissionDenied", call, user, err)
		}
	}
}

func TestRealInfraRemovedMemberIsDeniedAtOnceThroughTheActorCache(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	cached := time.Now()
	sendAs(t, client, "bob", roomID, "cache-bob-1", "bob is cached")

	removeAs(t, client, itUser, roomID, "bob")
	assertDeniedEverywhere(t, client, "bob", roomID)
	if took := time.Since(cached); took >= itMemberCacheTTL {
		t.Fatalf("denial came %v after bob was cached, want it before the %v cache TTL", took, itMemberCacheTTL)
	}
}

func TestRealInfraARemovalOnAnotherCoreReachesTheActorThroughTheMemberEvent(t *testing.T) {
	it := realInfra(t)
	core1 := startCore(t, it, itFastEffects)
	env := maps.Clone(itFastEffects)
	env["CORE_ID"] = "it-core-2-" + it.suffix
	core2 := startCore(t, it, env)
	awaitSlotsSplit(t, it, core1.cfg.CoreID, core2.cfg.CoreID)
	via1, via2 := dialCore(t, core1.cfg), dialCore(t, core2.cfg)
	roomID := createRoom(t, via1)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core1.cfg, roomID)
	forgets := itMetric(t, core1.cfg, itForgetsSeries)

	cached := time.Now()
	sendAs(t, via1, "bob", roomID, "watch-bob-1", "bob is cached on core-1")
	removed := removeAs(t, via2, itUser, roomID, "bob")
	awaitLiveEvent(t, live, pbconv.MemberEventID(room, "bob", removed.GetVer()))
	awaitMetric(t, core1.cfg, itForgetsSeries, func(v float64) bool { return v > forgets })

	_, err := via1.SendMessage(callerAs(t.Context(), "bob"), &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "watch-bob-2", Text: "after removal"})
	if took := time.Since(cached); status.Code(err) != codes.PermissionDenied || took >= itMemberCacheTTL {
		t.Fatalf("bob sends through core-1 %v after caching = %v, want PermissionDenied before the %v cache TTL", took, err, itMemberCacheTTL)
	}
}

func TestRealInfraAddMembersRetryWithTheSameRequestIDNeverReAddsARemovedUser(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)

	if added := addMembersAs(t, client, itUser, roomID, "erin-r", "erin"); len(added) != 1 || added[0].GetVer() != 1 {
		t.Fatalf("first add = %v, want erin at ver 1", added)
	}
	if removed := removeAs(t, client, itUser, roomID, "erin"); removed.GetVer() != 2 {
		t.Fatalf("remove = %v, want erin at ver 2", removed)
	}
	if again := addMembersAs(t, client, itUser, roomID, "erin-r", "erin"); len(again) != 0 {
		t.Fatalf("retry with the same request id = %v, want nothing added", again)
	}
	if fresh := addMembersAs(t, client, itUser, roomID, "erin-r2", "erin"); len(fresh) != 1 || fresh[0].GetVer() != 3 {
		t.Fatalf("add with a new request id = %v, want erin back at ver 3", fresh)
	}
	if docs := storedMembers(t, itStore(it, core), parseRoom(t, roomID), "erin"); !docs["erin"].Active() || docs["erin"].Ver != 3 {
		t.Fatalf("erin = %+v, want active at ver 3", docs["erin"])
	}
}

func TestRealInfraClearHistoryHidesByTimeAndSurvivesARejoin(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	sendAs(t, client, itUser, roomID, "rejoin-1", "before the clear 1")
	last := sendAs(t, client, itUser, roomID, "rejoin-2", "before the clear 2")
	cleared, err := client.ClearHistory(callerAs(t.Context(), "bob"), &chatimv1.ClearHistoryRequest{RoomId: roomID})
	if err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	mark := cleared.GetClearedAt().AsTime()

	removeAs(t, client, itUser, roomID, "bob")
	if back := addMembersAs(t, client, itUser, roomID, "rejoin-bob", "bob"); len(back) != 1 {
		t.Fatalf("rejoin = %v, want bob added back", back)
	}
	bob := storedMembers(t, itStore(it, core), room, "bob")["bob"]
	if !bob.ClearedAt.Equal(mark) || bob.ReadSeq != last {
		t.Fatalf("bob after rejoin = %+v, want cleared_at %v kept and read_seq %d", bob, mark, last)
	}
	after := sendAs(t, client, itUser, roomID, "rejoin-3", "after the rejoin")
	got := historyAs(t, client, "bob", roomID)
	if len(got) != 3 || !hiddenOnly(got[1]) || !hiddenOnly(got[2]) || got[after].GetHidden() || got[after].GetText() != "after the rejoin" {
		t.Fatalf("bob's history after rejoin = %v, want seq 1-2 hidden by the clear and seq %d visible", got, after)
	}
	if created := got[after].GetCreatedAt().AsTime(); !created.After(mark) {
		t.Fatalf("seq %d created at %v, not after the clear at %v", after, created, mark)
	}
}

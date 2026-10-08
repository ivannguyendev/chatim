package app

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itOwnerRaceRounds  = 20
	itRemoveRaceRounds = 10
)

func leaveAs(ctx context.Context, client chatimv1.CoreServiceClient, user, roomID string) (*chatimv1.LeaveRoomResponse, error) {
	return retryingUnavailable(callerAs(ctx, user), func(ctx context.Context) (*chatimv1.LeaveRoomResponse, error) {
		return client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: roomID})
	})
}

func removeRetrying(ctx context.Context, client chatimv1.CoreServiceClient, user, roomID, target string) (*chatimv1.RemoveMemberResponse, error) {
	return retryingUnavailable(callerAs(ctx, user), func(ctx context.Context) (*chatimv1.RemoveMemberResponse, error) {
		return client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: roomID, User: target})
	})
}

func activeOwners(members map[string]domain.Member) []string {
	var out []string
	for user, m := range members {
		if m.Active() && m.Role == domain.RoleOwner {
			out = append(out, user)
		}
	}
	return out
}

func atOnce(calls ...func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, call := range calls {
		wg.Go(func() {
			<-start
			call()
		})
	}
	close(start)
	wg.Wait()
}

func TestRealInfraTwoOwnersLeavingAtOnceKeepAnOwner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	st := itStore(it, core)
	for round := range itOwnerRaceRounds {
		roomID := createRoomWith(t, client, []string{itUser, "bob", "dave"})
		room := parseRoom(t, roomID)
		addMembersAs(t, client, itUser, roomID, "owners-"+strconv.Itoa(round), "carol")
		changeRole(t, client, roomID, "bob", chatimv1.MemberRole_MEMBER_ROLE_OWNER)
		changeRole(t, client, roomID, "carol", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
		changeRole(t, client, roomID, "dave", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)

		leavers := []string{itUser, "bob"}
		resps := make([]*chatimv1.LeaveRoomResponse, len(leavers))
		errs := make([]error, len(leavers))
		atOnce(
			func() { resps[0], errs[0] = leaveAs(t.Context(), client, leavers[0], roomID) },
			func() { resps[1], errs[1] = leaveAs(t.Context(), client, leavers[1], roomID) },
		)
		successors := []string{resps[0].GetNewOwner(), resps[1].GetNewOwner()}
		slices.Sort(successors)
		if errs[0] != nil || errs[1] != nil || !resps[0].GetChanged() || !resps[1].GetChanged() || !slices.Equal(successors, []string{"", "dave"}) {
			t.Fatalf("round %d: leaves = %v / %v, %v / %v; want both to leave and dave named successor once", round, resps[0], resps[1], errs[0], errs[1])
		}
		docs := storedMembers(t, st, room, itUser, "bob", "carol", "dave")
		if owners := activeOwners(docs); !slices.Equal(owners, []string{"dave"}) || docs[itUser].Active() || docs["bob"].Active() {
			t.Fatalf("round %d: active owners = %v, docs %+v; want only dave, the earliest admin, after both owners left", round, owners, docs)
		}
		if docs["carol"].Role != domain.RoleAdmin || !docs["carol"].Active() {
			t.Fatalf("round %d: carol = %+v, want an active admin", round, docs["carol"])
		}
		assertCountMatchesDocs(t, st, room, 2)
	}
}

func TestRealInfraTwoOwnersRemovingEachOtherLeaveOneOwner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	st := itStore(it, core)
	for round := range itRemoveRaceRounds {
		roomID := createRoomWith(t, client, []string{itUser, "bob", "carol"})
		room := parseRoom(t, roomID)
		changeRole(t, client, roomID, "bob", chatimv1.MemberRole_MEMBER_ROLE_OWNER)

		pairs := [][2]string{{itUser, "bob"}, {"bob", itUser}}
		errs := make([]error, len(pairs))
		atOnce(
			func() { _, errs[0] = removeRetrying(t.Context(), client, pairs[0][0], roomID, pairs[0][1]) },
			func() { _, errs[1] = removeRetrying(t.Context(), client, pairs[1][0], roomID, pairs[1][1]) },
		)
		winner := slices.IndexFunc(errs, func(err error) bool { return err == nil })
		if winner < 0 || status.Code(errs[1-winner]) != codes.PermissionDenied {
			t.Fatalf("round %d: removals = %v, want one success and one PermissionDenied", round, errs)
		}
		docs := storedMembers(t, st, room, itUser, "bob", "carol")
		if owners := activeOwners(docs); !slices.Equal(owners, []string{pairs[winner][0]}) {
			t.Fatalf("round %d: active owners = %v, want only the winner %s", round, owners, pairs[winner][0])
		}
		assertCountMatchesDocs(t, st, room, 2)
	}
}

func TestRealInfraTheHighestPriorityAdminSucceedsTheLastOwner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "carol"})
	addMembersAs(t, client, itUser, roomID, "priority-dave", "dave")
	changeRole(t, client, roomID, "carol", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	changeRole(t, client, roomID, "dave", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	set := &chatimv1.SetMemberPriorityRequest{RoomId: roomID, User: "dave", Priority: 5}
	if resp, err := client.SetMemberPriority(caller(t.Context()), set); err != nil || !resp.GetChanged() {
		t.Fatalf("SetMemberPriority(dave, 5) = %v, %v; want a change", resp, err)
	}

	resp, err := leaveAs(t.Context(), client, itUser, roomID)
	if err != nil || resp.GetNewOwner() != "dave" {
		t.Fatalf("LeaveRoom of the last owner = %v, %v; want dave, the later admin with the higher priority", resp, err)
	}
	docs := storedMembers(t, itStore(it, core), parseRoom(t, roomID), "carol", "dave")
	if owners := activeOwners(docs); !slices.Equal(owners, []string{"dave"}) || docs["carol"].Role != domain.RoleAdmin {
		t.Fatalf("owners = %v, carol = %+v; want dave as the only owner and carol still admin", owners, docs["carol"])
	}
	if !docs["dave"].JoinedAt.After(docs["carol"].JoinedAt) {
		t.Fatalf("dave joined at %v, carol at %v; want dave to have joined later", docs["dave"].JoinedAt, docs["carol"].JoinedAt)
	}
}

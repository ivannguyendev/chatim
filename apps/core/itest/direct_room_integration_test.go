package itest

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itDirectOpeners = 8

func openDirectAs(ctx context.Context, client chatimv1.CoreServiceClient, user, other string) (*chatimv1.OpenDirectRoomResponse, error) {
	return retryingUnavailable(callerAs(ctx, user), func(ctx context.Context) (*chatimv1.OpenDirectRoomResponse, error) {
		return client.OpenDirectRoom(ctx, &chatimv1.OpenDirectRoomRequest{OtherUser: other})
	})
}

func TestRealInfraConcurrentOpensOfOnePairOnTwoCoresMakeOneDirectRoom(t *testing.T) {
	it := realInfra(t)
	core1 := startCore(t, it, itFastEffects)
	env := maps.Clone(itFastEffects)
	env["CORE_ID"] = "it-core-2-" + it.suffix
	core2 := startCore(t, it, env)
	awaitSlotsSplit(t, it, core1.cfg.CoreID, core2.cfg.CoreID)
	clients := []chatimv1.CoreServiceClient{dialCore(t, core1.cfg), dialCore(t, core2.cfg)}
	pair := [][2]string{{itUser, "bob"}, {"bob", itUser}}

	resps := make([]*chatimv1.OpenDirectRoomResponse, itDirectOpeners)
	errs := make([]error, itDirectOpeners)
	calls := make([]func(), itDirectOpeners)
	for i := range calls {
		calls[i] = func() {
			who := pair[i%2]
			resps[i], errs[i] = openDirectAs(t.Context(), clients[(i/2)%2], who[0], who[1])
		}
	}
	atOnce(calls...)

	created := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("opener %d: %v", i, err)
		}
		if resps[i].GetCreated() {
			created++
		}
	}
	roomID := resps[0].GetRoom().GetId()
	if created == 0 || slices.ContainsFunc(resps, func(r *chatimv1.OpenDirectRoomResponse) bool { return r.GetRoom().GetId() != roomID }) {
		t.Fatalf("openers got rooms %v with %d created, want one room created once at least", resps, created)
	}
	room := parseRoom(t, roomID)
	st := itStore(it, core1)
	stored := assertCountMatchesDocs(t, st, room, 2)
	if stored.Type != domain.RoomDM {
		t.Fatalf("room %d has type %v, want a direct room", room, stored.Type)
	}
	members := storedMembers(t, st, room, itUser, "bob")
	if len(members) != 2 || len(activeOwners(members)) != 0 {
		t.Fatalf("direct room members = %+v, want two active members and no owner", members)
	}
	for user, m := range members {
		if !m.Active() || m.Role != domain.RoleMember || m.Ver != 1 {
			t.Fatalf("member %s = %+v, want active member at version 1", user, m)
		}
	}
	again, err := openDirectAs(t.Context(), clients[1], "bob", itUser)
	if err != nil || again.GetRoom().GetId() != roomID || again.GetCreated() {
		t.Fatalf("open once more = %v, %v; want room %s already open", again, err, roomID)
	}
}

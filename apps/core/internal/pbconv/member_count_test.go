package pbconv_test

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func groupRoom() domain.Room {
	return domain.Room{ID: 9_007_199_254_740_993, Tenant: "acme", Type: domain.RoomGroup, Name: "g", CreatedBy: "alice", CreatedAt: sentAt}
}

func TestMemberCountChangedCarriesTheCountAndItsVer(t *testing.T) {
	cases := map[string]struct {
		count int
		want  int32
	}{
		"plain":    {12, 12},
		"too big":  {math.MaxInt32 + 1, math.MaxInt32},
		"negative": {-1, 0},
	}
	for name, tc := range cases {
		want := memberEnvelope("9007199254740993-members-v8", "carol")
		want.Payload = &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{MemberCount: tc.want, MemberCountVer: 8}}
		got := pbconv.MemberCountChanged(groupRoom(), domain.MemberCount{Count: tc.count, Ver: 8}, "carol", changedAt)
		if !proto.Equal(got, want) {
			t.Fatalf("%s: MemberCountChanged = %v, want %v", name, got, want)
		}
	}
}

package route_test

import (
	"context"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type coreOnly struct {
	chatimv1.CoreServiceClient
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func mentionCalls() map[string]linkCase {
	return map[string]linkCase{
		"user groups": linkCaseOf(&chatimv1.SetUserGroupsRequest{Items: []*chatimv1.UserGroupsItem{{User: "an", Groups: []string{"team-sale"}, Ver: 2}}}, (*route.Client).SetUserGroups,
			&chatimv1.SetUserGroupsResponse{ChangedUsers: []string{"an"}}, false),
		"groups": linkCaseOf(&chatimv1.SetGroupsRequest{Items: []*chatimv1.GroupItem{{Id: "team-sale", Name: "Sale", Active: true, Ver: 1}}}, (*route.Client).SetGroups,
			&chatimv1.SetGroupsResponse{ChangedGroups: []string{"team-sale"}}, false),
		"room groups": linkCaseOf(&chatimv1.ListRoomGroupsRequest{RoomId: "42"}, (*route.Client).ListRoomGroups,
			&chatimv1.ListRoomGroupsResponse{Groups: []*chatimv1.RoomGroup{{Id: "team-sale", Name: "Sale"}}, Filtered: true}, false),
	}
}

func TestMentionCallsNeedTheMentionServiceOnTheDialedConnection(t *testing.T) {
	dial := func(string) (chatimv1.CoreServiceClient, io.Closer, error) { return coreOnly{}, nopCloser{}, nil }
	c, err := route.New(&splitLocator{}, dial, route.Policy{})
	if err != nil {
		t.Fatalf("route.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	calls := mentionCalls()
	calls["mentions"] = linkCaseOf(&chatimv1.ListMentionsRequest{}, (*route.Client).ListMentions, nil, false)
	for name, mc := range calls {
		if _, st, err := mc.call(context.Background(), c); status.Code(err) != codes.Unimplemented || st.Attempts != 1 {
			t.Fatalf("%s over a core-only client = %v after %d attempts, want Unimplemented at once", name, err, st.Attempts)
		}
	}
}

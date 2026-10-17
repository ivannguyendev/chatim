package route_test

import (
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

func TestListMentionsNeedsTheMentionServiceOnTheDialedConnection(t *testing.T) {
	dial := func(string) (chatimv1.CoreServiceClient, io.Closer, error) { return coreOnly{}, nopCloser{}, nil }
	c, err := route.New(&splitLocator{}, dial, route.Policy{})
	if err != nil {
		t.Fatalf("route.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, st, err := c.ListMentions(t.Context(), &chatimv1.ListMentionsRequest{}); status.Code(err) != codes.Unimplemented || st.Attempts != 1 {
		t.Fatalf("ListMentions over a core-only client = %v after %d attempts, want Unimplemented at once", err, st.Attempts)
	}
}

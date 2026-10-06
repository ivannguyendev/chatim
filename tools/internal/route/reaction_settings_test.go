package route_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func TestReactionSettingsGoToAnyCoreAndRetryAttemptTimeouts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core := &fakeCore{block: 1}
		loc := &fakeLocator{route: func(n int) (string, bool) { return "core-1:9000", n > 0 }}
		c := newClient(t, loc, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{Attempt: time.Second})
		resp, st, err := c.GetReactionSettings(t.Context(), &chatimv1.GetReactionSettingsRequest{})
		if err != nil || st.Attempts != 3 || st.Addr != "core-1:9000" || !slices.Equal(resp.GetEmojis(), []string{"👍", "❤️"}) {
			t.Fatalf("GetReactionSettings = %v, %v after %d attempts on %q; want the list on the third attempt", resp, err, st.Attempts, st.Addr)
		}
	})
}

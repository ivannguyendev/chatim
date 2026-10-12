package actor_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestSendAsksThePolicyBeforeWriting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var got []access.Request
		deny := access.PolicyFunc(func(_ context.Context, r access.Request) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, r)
			return access.ErrDenied
		})
		rg := newRig(t, baseConfig, actor.WithPolicy(deny))
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, apperr.ErrPermissionDenied)
		if n := len(rg.sub.sent()); n != 0 {
			t.Fatalf("denied send reached the flusher %d times", n)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(got) != 1 || got[0].Action != access.SendMessage || got[0].User != "alice" || got[0].Room.ID != roomA || got[0].Member.User != "alice" {
			t.Fatalf("policy saw %+v, want one send_message by member alice in room %d", got, roomA)
		}
	})
}

func TestDefaultPolicyAllowsMembers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.start(t)
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1")); ack.Seq != 1 {
			t.Fatalf("seq = %d, want 1", ack.Seq)
		}
	})
}

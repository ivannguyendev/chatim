package actor_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func tombstone(ctx context.Context, rg *rig, room uint64, user string) error {
	cur, err := rg.rooms.Rooms.Member(ctx, room, user)
	if err != nil {
		return fmt.Errorf("member %s: %w", user, err)
	}
	next := cur.Next(cur.Role, domain.MemberRemoved, cur.Priority, "remove-"+user, "alice", time.Now())
	if ok, err := rg.rooms.ApplyMember(ctx, cur, next); !ok || err != nil {
		return fmt.Errorf("remove %s: written %v, err %w", user, ok, err)
	}
	return nil
}

func removeMember(t *testing.T, rg *rig, room uint64, user string) {
	t.Helper()
	if err := tombstone(t.Context(), rg, room, user); err != nil {
		t.Fatal(err)
	}
}

func addMember(t *testing.T, rg *rig, room uint64, user string) {
	t.Helper()
	j := domain.Join{Room: room, Tenant: tenant, RequestID: "add-" + user, By: "alice", At: time.Now()}
	if res, err := rg.rooms.AddMembers(t.Context(), j, []string{user}); err != nil || res.Changed != 1 {
		t.Fatalf("AddMembers(%s) = %+v, %v", user, res, err)
	}
}

func expectDenied(t *testing.T, rg *rig, c actor.SendCmd) {
	t.Helper()
	_, err := rg.Send(t.Context(), c)
	expectErr(t, err, apperr.ErrPermissionDenied)
}

func TestForgetMembersDropsARemovedMemberAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		removeMember(t, rg, roomA, "bob")
		rg.ForgetMembers(roomA)
		expectDenied(t, rg, cmd(roomA, "bob", "b2"))
	})
}

func TestCachedMembershipExpiresAfterTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		removeMember(t, rg, roomA, "bob")
		time.Sleep(actor.MemberCacheTTL - time.Millisecond)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b2"))
		time.Sleep(time.Millisecond)
		expectDenied(t, rg, cmd(roomA, "bob", "b3"))
	})
}

func TestARemovedMemberIsNeverCachedSoAReAddWorksAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		removeMember(t, rg, roomA, "bob")
		rg.ForgetMembers(roomA)
		before := rg.rooms.memberCalls()
		expectDenied(t, rg, cmd(roomA, "bob", "b2"))
		expectDenied(t, rg, cmd(roomA, "bob", "b3"))
		if n := rg.rooms.memberCalls() - before; n != 2 {
			t.Fatalf("removed member looked up %d times, want 2 (never cached)", n)
		}
		addMember(t, rg, roomA, "bob")
		mustSend(t, rg.Router, cmd(roomA, "bob", "b4"))
	})
}

func TestForgetMembersWithoutAnActorDoesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.ForgetMembers(roomA)
		rg.start(t)
		rg.ForgetMembers(roomA)
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("ForgetMembers started %d actors, want 0", n)
		}
		mustSend(t, rg.Router, cmd(roomA, "alice", "a1"))
		rg.ForgetMembers(roomB)
		mustSend(t, rg.Router, cmd(roomA, "alice", "a2"))
		if n := rg.rooms.memberCalls(); n != 1 {
			t.Fatalf("member lookups = %d, want 1 (forgetting another room keeps this cache)", n)
		}
	})
}

func TestAForgetDuringAMemberReadDropsThatRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		var once sync.Once
		rg.rooms.setMemberHook(func(ctx context.Context) {
			once.Do(func() {
				if err := tombstone(ctx, rg, roomA, "bob"); err != nil {
					t.Error(err)
				}
				rg.ForgetMembers(roomA)
			})
		})
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		expectDenied(t, rg, cmd(roomA, "bob", "b2"))
	})
}

func TestForgetMembersIsSafeAlongsideSends(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := baseConfig
		cfg.Mailbox = 64
		rg := started(t, cfg)
		stop := make(chan struct{})
		forgot := make(chan struct{})
		go func() {
			defer close(forgot)
			for {
				select {
				case <-stop:
					return
				default:
					rg.ForgetMembers(roomA)
					runtime.Gosched()
				}
			}
		}()
		var waits []<-chan sendResult
		for i := range 32 {
			user := []string{"alice", "bob"}[i%2]
			waits = append(waits, sendAsync(t.Context(), rg.Router, cmd(roomA, user, fmt.Sprintf("c%d", i))))
		}
		for _, w := range waits {
			if res := <-w; res.err != nil {
				t.Errorf("send failed: %v", res.err)
			}
		}
		close(stop)
		<-forgot
	})
}

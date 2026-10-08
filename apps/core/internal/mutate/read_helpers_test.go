package mutate_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type countingLast struct {
	mutate.Messages
	lasts atomic.Int32
}

func (c *countingLast) Last(ctx context.Context, room, thread uint64) (uint64, error) {
	c.lasts.Add(1)
	return c.Messages.Last(ctx, room, thread)
}

func readCmd(user string, r, seq uint64) mutate.ReadCmd {
	return mutate.ReadCmd{Tenant: tenant, User: user, Room: r, Seq: seq}
}

func (rg *rig) markRead(t *testing.T, user string, r, seq uint64) domain.ReadPosition {
	t.Helper()
	pos, err := rg.m.MarkRead(t.Context(), readCmd(user, r, seq))
	if err != nil {
		t.Fatalf("MarkRead(%s, %d): %v", user, seq, err)
	}
	return pos
}

func (rg *rig) markUnread(t *testing.T, user string, r, seq uint64) domain.ReadPosition {
	t.Helper()
	pos, err := rg.m.MarkUnread(t.Context(), readCmd(user, r, seq))
	if err != nil {
		t.Fatalf("MarkUnread(%s, %d): %v", user, seq, err)
	}
	return pos
}

func (rg *rig) memberOf(t *testing.T, r uint64, user string) domain.Member {
	t.Helper()
	docs, err := rg.rooms.MembersOf(t.Context(), r, []string{user})
	if err != nil || len(docs) != 1 {
		t.Fatalf("doc of %s in %d = %v, %v", user, r, docs, err)
	}
	return docs[0]
}

func (rg *rig) sendMany(t *testing.T, n uint64) {
	t.Helper()
	for seq := uint64(1); seq <= n; seq++ {
		rg.send(t, seq, "alice", "m")
	}
}

func (rg *rig) setHead(t *testing.T, seq uint64) {
	t.Helper()
	if err := rg.rooms.TouchActivity(t.Context(), []store.Activity{{Room: room, Seq: seq, At: created}}); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
}

func withoutReadFields(m domain.Member) domain.Member {
	m.ReadSeq, m.ReadVer, m.LastChangeAt = 0, 0, time.Time{}
	return m
}

package actor_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var leftMeta = []byte(`{"event":"member_left","user":"lan"}`)

func systemCmd(user, cid string) actor.SendCmd {
	c := cmd(roomA, user, cid)
	c.Kind, c.SkipUnread, c.Meta = domain.KindSystem, true, leftMeta
	return c
}

func TestSystemMessageFromANonMemberIsWrittenWithoutAMemberLookup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var got []access.Request
		spy := access.PolicyFunc(func(_ context.Context, r access.Request) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, r)
			return nil
		})
		rg := newRig(t, baseConfig, actor.WithPolicy(spy))
		rg.start(t)
		if ack := mustSend(t, rg.Router, systemCmd("sysmsg-bot", "sys-1")); ack.Seq != 1 {
			t.Fatalf("seq = %d, want 1", ack.Seq)
		}
		if n := rg.rooms.memberCalls(); n != 0 {
			t.Fatalf("system message looked up membership %d times, want 0", n)
		}
		mu.Lock()
		if len(got) != 1 || got[0].Action != access.SendSystemMessage || got[0].User != "sysmsg-bot" || got[0].Room.ID != roomA || got[0].Member != (domain.Member{}) {
			t.Fatalf("policy saw %+v, want one send_system_message with no member", got)
		}
		mu.Unlock()
		docs := storedCIDs(t, rg.msgs.Messages, roomA)["sys-1"]
		if len(docs) != 1 || docs[0].Kind != domain.KindSystem || !docs[0].SkipUnread || !bytes.Equal(docs[0].Meta, leftMeta) || docs[0].From != "sysmsg-bot" {
			t.Fatalf("stored %+v, want one system message with skip_unread and meta", docs)
		}
		synctest.Wait()
		evs := rg.events.events(roomA)
		if len(evs) != 1 {
			t.Fatalf("events = %v, want one msg_created", evs)
		}
		m := evs[0].GetMessageCreated().GetMessage()
		if m.GetKind() != chatimv1.MessageKind_MESSAGE_KIND_SYSTEM || !m.GetSkipUnread() || !bytes.Equal(m.GetMeta(), leftMeta) {
			t.Fatalf("msg_created = %v, want kind system, skip_unread and meta", m)
		}
	})
}

func TestSystemMessageDedupesItsCID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		first := mustSend(t, rg.Router, systemCmd("sysmsg-bot", "sys-1"))
		again := mustSend(t, rg.Router, systemCmd("sysmsg-bot", "sys-1"))
		if !sameAck(first, again) || len(storedCIDs(t, rg.msgs.Messages, roomA)["sys-1"]) != 1 {
			t.Fatalf("resend ack %+v vs %+v, want the same single write", again, first)
		}
	})
}

func TestTextFromANonMemberIsStillDenied(t *testing.T) {
	rg := started(t, baseConfig)
	_, err := rg.Send(context.Background(), cmd(roomA, "sysmsg-bot", "c1"))
	expectErr(t, err, domain.ErrNotMember)
}

func TestSystemMessageStillChecksTheTenantAndThePolicy(t *testing.T) {
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		if r.Action == access.SendSystemMessage {
			return access.ErrDenied
		}
		return nil
	})
	rg := newRig(t, baseConfig, actor.WithPolicy(deny))
	rg.start(t)
	_, err := rg.Send(context.Background(), systemCmd("alice", "sys-1"))
	expectErr(t, err, apperr.ErrPermissionDenied)
	foreign := systemCmd("sysmsg-bot", "sys-2")
	foreign.Tenant = "other"
	_, err = rg.Send(context.Background(), foreign)
	expectErr(t, err, domain.ErrRoomNotFound)
	if len(rg.sub.sent()) != 0 {
		t.Fatal("a refused system message reached the flusher")
	}
}

func TestSendRejectsBadSystemMessagesAndMetaBeforeRouting(t *testing.T) {
	rg := started(t, baseConfig)
	tests := map[string]struct {
		cmd   func() actor.SendCmd
		field string
	}{
		"system reply": {func() actor.SendCmd {
			c := systemCmd("bot", "s1")
			c.ReplyTo = &domain.ReplyRef{Seq: 1}
			return c
		}, "reply_to"},
		"system forward": {func() actor.SendCmd {
			c := systemCmd("bot", "s1")
			c.Forward = &domain.ForwardRef{Room: roomB, Seq: 9, Author: "lan"}
			return c
		}, "forward_from"},
		"system mention": {func() actor.SendCmd {
			c := systemCmd("bot", "s1")
			c.Mentions = []domain.MentionTarget{minh}
			return c
		}, "mentions"},
		"system @all": {func() actor.SendCmd {
			c := systemCmd("bot", "s1")
			c.MentionAll = true
			return c
		}, "mentions"},
		"meta over 8KB": {func() actor.SendCmd {
			c := cmd(roomA, "alice", "c1")
			c.Meta = bytes.Repeat([]byte("a"), 8193)
			return c
		}, "meta"},
		"unknown kind": {func() actor.SendCmd {
			c := cmd(roomA, "alice", "c1")
			c.Kind = 9
			return c
		}, "kind"},
	}
	for name, tt := range tests {
		_, err := rg.Send(context.Background(), tt.cmd())
		if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+tt.field) {
			t.Errorf("%s: Send = %v, want ErrInvalidArgument naming %q", name, err, tt.field)
		}
	}
	if n := rg.ActorCount(); n != 0 {
		t.Errorf("%d actors started for invalid commands, want 0", n)
	}
}

func TestTextMessageCarriesMeta(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		c := cmd(roomA, "alice", "c1")
		c.Meta = bytes.Repeat([]byte("m"), 8192)
		mustSend(t, rg.Router, c)
		docs := storedCIDs(t, rg.msgs.Messages, roomA)["c1"]
		if len(docs) != 1 || docs[0].Kind != domain.KindText || docs[0].SkipUnread || !bytes.Equal(docs[0].Meta, c.Meta) {
			t.Fatalf("stored %+v, want a text message with 8KB of meta", docs)
		}
	})
}

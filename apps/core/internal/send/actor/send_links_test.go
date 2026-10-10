package actor_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	minh = domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}
	team = domain.MentionTarget{Kind: domain.MentionGroup, ID: "team-design"}
)

func linkedCmd(cid string) actor.SendCmd {
	c := cmd(roomA, "alice", cid)
	c.ReplyTo = &domain.ReplyRef{Seq: 1}
	c.Forward = &domain.ForwardRef{Room: roomB, Seq: 9, Author: "lan"}
	c.Mentions = []domain.MentionTarget{minh, team, minh}
	c.MentionAll = true
	return c
}

func TestSentMessageCarriesRepliesForwardsAndMentions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, linkedCmd("c1"))
		synctest.Wait()
		sent := rg.sub.sent()
		if len(sent) != 1 || len(sent[0]) != 1 {
			t.Fatalf("submitted %v, want one message", sent)
		}
		m := sent[0][0]
		if *m.ReplyTo != (domain.ReplyRef{Seq: 1}) || m.Forward.Author != "lan" || !m.MentionAll ||
			len(m.Mentions) != 2 || m.Mentions[0] != minh || m.Mentions[1] != team {
			t.Fatalf("stored %+v, want reply, forward, @all and two distinct mentions", m)
		}
		evs := rg.events.events(roomA)
		if len(evs) != 1 {
			t.Fatalf("events = %v, want one msg_created", evs)
		}
		got := evs[0].GetMessageCreated().GetMessage()
		wantMentions := []*chatimv1.MentionTarget{
			{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "minh"},
			{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: "team-design"},
		}
		if !proto.Equal(got.GetReplyTo(), &chatimv1.ReplyRef{Seq: 1}) || got.GetForwardFrom().GetAuthor() != "lan" || !got.GetMentionAll() ||
			len(got.GetMentionTargets()) != 2 || !proto.Equal(got.GetMentionTargets()[0], wantMentions[0]) || !proto.Equal(got.GetMentionTargets()[1], wantMentions[1]) {
			t.Fatalf("msg_created = %v, want reply_to, forward_from and mentions", got)
		}
	})
}

func TestSendRejectsBadLinksBeforeRouting(t *testing.T) {
	cfg := baseConfig
	cfg.MentionTargets = 2
	rg := started(t, cfg)
	tests := map[string]func(*actor.SendCmd){
		"three mentions": func(c *actor.SendCmd) {
			c.Mentions = []domain.MentionTarget{minh, team, {Kind: domain.MentionUser, ID: "lan"}}
		},
		"unknown mention kind": func(c *actor.SendCmd) { c.Mentions = []domain.MentionTarget{{ID: "minh"}} },
		"reply in a thread":    func(c *actor.SendCmd) { c.ReplyTo = &domain.ReplyRef{Thread: 1, Seq: 1} },
		"reply to seq 0":       func(c *actor.SendCmd) { c.ReplyTo = &domain.ReplyRef{} },
	}
	for name, mutate := range tests {
		c := cmd(roomA, "alice", "c1")
		mutate(&c)
		if _, err := rg.Send(context.Background(), c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Send = %v, want ErrInvalidArgument", name, err)
		}
	}
	c := cmd(roomA, "alice", "c2")
	c.Mentions = []domain.MentionTarget{minh, team, minh}
	mustSend(t, rg.Router, c)
}

func TestMentionAllAsksThePolicy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var asked []access.Action
		noAll := access.PolicyFunc(func(_ context.Context, r access.Request) error {
			asked = append(asked, r.Action)
			if r.Action == access.MentionAll {
				return access.ErrDenied
			}
			return nil
		})
		rg := newRig(t, baseConfig, actor.WithPolicy(noAll))
		rg.start(t)
		c := cmd(roomA, "alice", "c1")
		c.MentionAll = true
		_, err := rg.Send(t.Context(), c)
		expectErr(t, err, apperr.ErrPermissionDenied)
		mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
		if len(asked) != 3 || asked[0] != access.SendMessage || asked[1] != access.MentionAll || asked[2] != access.SendMessage {
			t.Fatalf("policy asked %v, want send_message, mention_all, send_message", asked)
		}
		if n := len(rg.sub.sent()); n != 1 {
			t.Fatalf("submitted %d groups, want only the plain send", n)
		}
	})
}

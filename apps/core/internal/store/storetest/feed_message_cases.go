package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func feedReplyMentionFlags(t *testing.T, msgs store.Messages, _ store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	plain, reply, mention, all, both, forward := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2), msg(roomA, mainThread, 3),
		msg(roomA, mainThread, 4), msg(roomA, mainThread, 5), msg(roomA, mainThread, 6)
	users := []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}}
	reply.ReplyTo = &domain.ReplyRef{Seq: 1}
	mention.Mentions = users
	all.MentionAll = true
	both.ReplyTo, both.Mentions = &domain.ReplyRef{Seq: 1}, users
	forward.Forward = &domain.ForwardRef{Room: roomB, Seq: 9, Author: "lan", SentAt: baseTime}
	insertEach(t, msgs, plain, reply, mention, all, both, forward)
	want := []store.ReplyMentionFlags{0, store.HasReply, store.HasMention, store.HasMention, store.HasReply | store.HasMention, 0}
	for i, c := range nextChanges(t, cur, len(want)) {
		if c.Kind != store.MessageInserted || c.Msg.Seq != uint64(i+1) || c.ReplyMentionFlags != want[i] {
			t.Fatalf("change %d = kind %d seq %d flags %d, want a message seq %d with flags %d", i, c.Kind, c.Msg.Seq, c.ReplyMentionFlags, i+1, want[i])
		}
	}
}

package mongostore

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestDecodeChangeFlagsRepliesAndMentions(t *testing.T) {
	reply := &domain.ReplyRef{Seq: 7}
	users := []domain.MentionTarget{{Kind: domain.MentionGroup, ID: "dev"}}
	cases := map[string]struct {
		mutate func(*domain.Message)
		want   store.ReplyMentionFlags
	}{
		"plain":           {func(*domain.Message) {}, 0},
		"reply":           {func(m *domain.Message) { m.ReplyTo = reply }, store.HasReply},
		"group mention":   {func(m *domain.Message) { m.Mentions = users }, store.HasMention},
		"mention all":     {func(m *domain.Message) { m.MentionAll = true }, store.HasMention},
		"reply mentioned": {func(m *domain.Message) { m.ReplyTo, m.Mentions = reply, users }, store.HasReply | store.HasMention},
	}
	for name, c := range cases {
		m := sampleMessage()
		c.mutate(&m)
		doc, err := encodeMessage(m)
		if err != nil {
			t.Fatalf("%s: encodeMessage: %v", name, err)
		}
		got, err := decodeChange(changeOn(t, messagesCollection, doc))
		if err != nil || got.Kind != store.MessageInserted || got.ReplyMentionFlags != c.want {
			t.Errorf("%s: change = %+v, %v; want flags %d", name, got, err, c.want)
		}
	}
}

func TestDecodeChangeRejectsAMessageWithBrokenLinks(t *testing.T) {
	doc, err := encodeMessage(sampleMessage())
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	broken := bson.E{Key: "rp", Value: bson.D{{Key: "th", Value: int64(0)}, {Key: "s", Value: int64(-1)}}}
	var fields bson.D
	if err := bson.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, err := decodeChange(changeOn(t, messagesCollection, append(fields, broken))); !errors.Is(err, errCorrupt) {
		t.Fatalf("message with a negative reply seq = %v, want errCorrupt", err)
	}
}

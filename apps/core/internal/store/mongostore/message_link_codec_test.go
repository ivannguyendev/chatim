package mongostore

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func linkedMessage() domain.Message {
	m := sampleMessage()
	m.ReplyTo = &domain.ReplyRef{Seq: 40}
	m.Forward = &domain.ForwardRef{Room: 555, Seq: 9, Author: "lan", SentAt: codecTime}
	m.Mentions = []domain.MentionTarget{{Kind: domain.MentionUser, ID: "minh"}, {Kind: domain.MentionGroup, ID: "team-design"}}
	m.MentionAll = true
	return m
}

func TestMessageCodecRoundTripsLinksWithShortNames(t *testing.T) {
	m := linkedMessage()
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts", "rp", "fw", "mt", "ma"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if got := raw.Lookup("mt", "1", "k").StringValue(); got != "group" {
		t.Fatalf("mt.1.k = %q, want group", got)
	}
	if got := raw.Lookup("fw", "f").StringValue(); got != "lan" {
		t.Fatalf("fw.f = %q, want lan", got)
	}
	got, err := decodeMessage(back)
	if err != nil {
		t.Fatalf("decodeMessage: %v", err)
	}
	if !got.CreatedAt.Equal(m.CreatedAt) || !got.Forward.SentAt.Equal(m.Forward.SentAt) {
		t.Fatalf("times = %v / %v, want %v", got.CreatedAt, got.Forward.SentAt, codecTime)
	}
	got.CreatedAt, got.Forward.SentAt = m.CreatedAt, m.Forward.SentAt
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("decoded %+v, want %+v", got, m)
	}
}

func TestEncodeMessageNeverWritesTheReplyCount(t *testing.T) {
	m := sampleMessage()
	m.Replies = domain.ReplyCount{N: 3, Version: 4}
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	if _, raw := roundTrip(t, doc); slices.Contains(fieldNames(t, raw), "rc") {
		t.Fatal("insert document carries rc")
	}
	doc.Replies = &replyCountDoc{N: 3, Version: 4}
	got, err := decodeMessage(doc)
	if err != nil || got.Replies != m.Replies {
		t.Fatalf("decode rc = %+v, %v; want %+v", got.Replies, err, m.Replies)
	}
}

func TestDecodeMessageReadsOldDocumentsWithoutLinks(t *testing.T) {
	raw, err := bson.Marshal(bson.D{
		{Key: "_id", Value: bson.Binary{Data: keys.Msg(7, 0, 1)}}, {Key: "t", Value: "acme"}, {Key: "f", Value: "alice"},
		{Key: "k", Value: int32(domain.KindText)}, {Key: "x", Value: "old"}, {Key: "c", Value: "c-1"}, {Key: "ts", Value: codecTime},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var d messageDoc
	if err := bson.Unmarshal(raw, &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	m, err := decodeMessage(d)
	if err != nil || m.ReplyTo != nil || m.Forward != nil || m.Mentions != nil || m.MentionAll || m.Replies != (domain.ReplyCount{}) {
		t.Fatalf("old document decoded as %+v, %v; want no links", m, err)
	}
}

func TestMessageLinkCodecRejectsBadValues(t *testing.T) {
	bad := sampleMessage()
	bad.Mentions = []domain.MentionTarget{{Kind: 9, ID: "x"}}
	if _, err := encodeMessage(bad); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("encode unknown mention kind = %v, want ErrInvalidArgument", err)
	}
	good, err := encodeMessage(linkedMessage())
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	corrupt := map[string]func(d *messageDoc){
		"reply seq":     func(d *messageDoc) { d.ReplyTo = &replyDoc{Seq: -1} },
		"forward room":  func(d *messageDoc) { d.Forward = &forwardDoc{Room: -5, Seq: 1} },
		"reply count n": func(d *messageDoc) { d.Replies = &replyCountDoc{N: -1} },
	}
	for name, f := range corrupt {
		d := good
		f(&d)
		if _, err := decodeMessage(d); !errors.Is(err, errCorrupt) {
			t.Fatalf("%s: decodeMessage = %v, want errCorrupt", name, err)
		}
	}
}

func TestDecodeMessageSkipsUnknownMentionKinds(t *testing.T) {
	raw, err := bson.Marshal(bson.D{
		{Key: "_id", Value: bson.Binary{Data: keys.Msg(7, 0, 1)}}, {Key: "t", Value: "acme"}, {Key: "f", Value: "alice"},
		{Key: "k", Value: int32(domain.KindText)}, {Key: "x", Value: "hi"}, {Key: "c", Value: "c-1"}, {Key: "ts", Value: codecTime},
		{Key: "mt", Value: bson.A{bson.D{{Key: "k", Value: "team"}, {Key: "i", Value: "x"}}, bson.D{{Key: "k", Value: "user"}, {Key: "i", Value: "minh"}}}},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var d messageDoc
	if err := bson.Unmarshal(raw, &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	m, err := decodeMessage(d)
	if err != nil || len(m.Mentions) != 1 || m.Mentions[0] != (domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}) {
		t.Fatalf("decoded mentions %+v, %v; want only user minh", m.Mentions, err)
	}
}

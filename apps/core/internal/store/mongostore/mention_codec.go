package mongostore

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const (
	mentionLive    int64 = 1
	mentionRetired int64 = 2
	mentionRead    int64 = 3
)

type mentionLinkDoc struct {
	ID        []byte    `bson:"_id"`
	Key       []byte    `bson:"message_key"`
	Room      int64     `bson:"room_id"`
	Tenant    string    `bson:"tenant"`
	Target    string    `bson:"target"`
	Kind      string    `bson:"kind"`
	Thread    int64     `bson:"thread_root"`
	Seq       int64     `bson:"seq"`
	Sender    string    `bson:"sender_id"`
	State     int64     `bson:"state"`
	Ver       int64     `bson:"message_ver"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

var mentionKindNames = map[domain.MentionKind]string{
	domain.MentionUser:  "user",
	domain.MentionGroup: "group",
	domain.MentionAll:   "all",
}

var mentionKeyKinds = map[domain.MentionKind]keys.MentionKind{
	domain.MentionUser:  keys.MentionUserKind,
	domain.MentionGroup: keys.MentionGroupKind,
	domain.MentionAll:   keys.MentionAllKind,
}

func mentionID(key store.MsgKey, t domain.MentionTarget) []byte {
	return keys.Mention(msgID(key), mentionKeyKinds[t.Kind], t.ID)
}

type mentionPlace struct {
	room, thread, seq int64
}

func placeOf(key store.MsgKey) (mentionPlace, error) {
	room, err := toInt64("room id", key.Room)
	if err != nil {
		return mentionPlace{}, err
	}
	thread, err := toInt64("thread", key.Thread)
	if err != nil {
		return mentionPlace{}, err
	}
	seq, err := toInt64("seq", key.Seq)
	return mentionPlace{room: room, thread: thread, seq: seq}, err
}

func liveMentionUpdate(set store.MentionSet, t domain.MentionTarget, at mentionPlace) mongo.Pipeline {
	fields := bson.D{
		{Key: "message_key", Value: msgID(set.Key)},
		{Key: "room_id", Value: at.room},
		{Key: "tenant", Value: set.Tenant},
		{Key: "target", Value: t.Name(set.Key.Room)},
		{Key: "kind", Value: mentionKindNames[t.Kind]},
		{Key: "thread_root", Value: at.thread},
		{Key: "seq", Value: at.seq},
		{Key: "sender_id", Value: set.Sender},
		{Key: "message_ver", Value: int64(set.Ver)},
		{Key: "created_at", Value: set.CreatedAt},
		{Key: "updated_at", Value: set.At},
	}
	stage := make(bson.D, 0, len(fields)+1)
	for _, f := range fields {
		stage = append(stage, bson.E{Key: f.Key, Value: bson.D{{Key: "$literal", Value: f.Value}}})
	}
	keepRead := bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$state", mentionRead}}}, mentionRead, mentionLive}}}
	stage = append(stage, bson.E{Key: "state", Value: keepRead})
	return mongo.Pipeline{{{Key: "$set", Value: stage}}}
}

func retiredMentionFields(set store.MentionSet) bson.D {
	return bson.D{
		{Key: "state", Value: mentionRetired},
		{Key: "message_ver", Value: int64(set.Ver)},
		{Key: "updated_at", Value: set.At},
	}
}

func decodeMention(d mentionLinkDoc) (domain.Mention, error) {
	msgKey, kind, id, err := keys.ParseMention(d.ID)
	if err != nil {
		return domain.Mention{}, fmt.Errorf("%w: mention _id: %w", errCorrupt, err)
	}
	room, thread, seq, _ := keys.ParseMsg(msgKey)
	ver, err := narrowUint32("mention message_ver", d.Ver)
	if err != nil {
		return domain.Mention{}, err
	}
	if d.State != mentionLive && d.State != mentionRetired && d.State != mentionRead {
		return domain.Mention{}, fmt.Errorf("%w: mention state %d", errCorrupt, d.State)
	}
	return domain.Mention{
		Key: domain.MsgKey{Room: room, Thread: thread, Seq: seq}, Tenant: d.Tenant,
		Target: domain.MentionTarget{Kind: domain.MentionKind(kind), ID: id}, Sender: d.Sender,
		Live: d.State != mentionRetired, Read: d.State == mentionRead, Ver: ver, CreatedAt: d.CreatedAt.UTC(), UpdatedAt: d.UpdatedAt.UTC(),
	}, nil
}

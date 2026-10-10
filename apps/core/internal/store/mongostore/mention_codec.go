package mongostore

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const (
	mentionLive    int64 = 1
	mentionRetired int64 = 2
)

type mentionLinkDoc struct {
	ID        []byte    `bson:"_id"`
	Key       []byte    `bson:"message_key"`
	Room      int64     `bson:"room_id"`
	Tenant    string    `bson:"tenant"`
	Target    string    `bson:"target"`
	Sender    string    `bson:"sender_id"`
	State     int64     `bson:"state"`
	Ver       int64     `bson:"message_ver"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

var mentionKeyKinds = map[domain.MentionKind]keys.MentionKind{
	domain.MentionUser:  keys.MentionUserKind,
	domain.MentionGroup: keys.MentionGroupKind,
	domain.MentionAll:   keys.MentionAllKind,
}

func mentionID(key store.MsgKey, t domain.MentionTarget) []byte {
	return keys.Mention(msgID(key), mentionKeyKinds[t.Kind], t.ID)
}

func liveMentionFields(set store.MentionSet, t domain.MentionTarget, room int64) bson.D {
	return bson.D{
		{Key: "message_key", Value: msgID(set.Key)},
		{Key: "room_id", Value: room},
		{Key: "tenant", Value: set.Tenant},
		{Key: "target", Value: t.Name(set.Key.Room)},
		{Key: "sender_id", Value: set.Sender},
		{Key: "state", Value: mentionLive},
		{Key: "message_ver", Value: int64(set.Ver)},
		{Key: "created_at", Value: set.CreatedAt},
		{Key: "updated_at", Value: set.At},
	}
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
	if d.State != mentionLive && d.State != mentionRetired {
		return domain.Mention{}, fmt.Errorf("%w: mention state %d", errCorrupt, d.State)
	}
	return domain.Mention{
		Key: domain.MsgKey{Room: room, Thread: thread, Seq: seq}, Tenant: d.Tenant,
		Target: domain.MentionTarget{Kind: domain.MentionKind(kind), ID: id}, Sender: d.Sender,
		Live: d.State == mentionLive, Ver: ver, CreatedAt: d.CreatedAt.UTC(), UpdatedAt: d.UpdatedAt.UTC(),
	}, nil
}

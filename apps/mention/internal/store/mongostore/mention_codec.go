package mongostore

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type mentionDoc struct {
	ID         []byte    `bson:"_id"`
	Key        []byte    `bson:"message_key"`
	Room       int64     `bson:"room_id"`
	Tenant     string    `bson:"tenant"`
	Kind       string    `bson:"kind"`
	Target     string    `bson:"target"`
	Thread     int64     `bson:"thread_root"`
	Seq        int64     `bson:"seq"`
	Sender     string    `bson:"sender_id"`
	State      int64     `bson:"state"`
	MessageVer int64     `bson:"message_ver"`
	CreatedAt  time.Time `bson:"created_at"`
	UpdatedAt  time.Time `bson:"updated_at"`
}

var targetKindNames = map[domain.TargetKind]string{
	domain.TargetUser:  "user",
	domain.TargetGroup: "group",
	domain.TargetAll:   "all",
}

var mentionStates = map[int64]domain.State{
	int64(domain.StateLive):    domain.StateLive,
	int64(domain.StateRetired): domain.StateRetired,
}

type mentionPlace struct {
	key               []byte
	room, thread, seq int64
}

func placeOf(key domain.MessageKey) (mentionPlace, error) {
	room, err := toInt64("room id", key.Room)
	if err != nil {
		return mentionPlace{}, err
	}
	thread, err := toInt64("thread", key.Thread)
	if err != nil {
		return mentionPlace{}, err
	}
	seq, err := toInt64("seq", key.Seq)
	return mentionPlace{key: messageID(key), room: room, thread: thread, seq: seq}, err
}

func messageID(key domain.MessageKey) []byte { return keys.Msg(key.Room, key.Thread, key.Seq) }

func mentionID(at mentionPlace, t domain.Target) []byte {
	return keys.Mention(at.key, keys.MentionKind(t.Kind), t.ID)
}

func mentionUpdate(p domain.Plan, at mentionPlace, t domain.Target, state domain.State, now time.Time) mongo.Pipeline {
	ver := int64(p.Ver)
	writable := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$tenant", p.Tenant}}}, p.Tenant}}},
		bson.D{{Key: "$lte", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$message_ver", ver}}}, ver}}},
	}}}
	var created any = literal(t.At)
	if state == domain.StateRetired {
		created = bson.D{{Key: "$ifNull", Value: bson.A{"$created_at", t.At}}}
	}
	differs := func(field string, v any) bson.D { return bson.D{{Key: "$ne", Value: bson.A{"$" + field, v}}} }
	changed := bson.D{{Key: "$and", Value: bson.A{writable, bson.D{{Key: "$or", Value: bson.A{
		differs("state", int64(state)), differs("message_ver", ver), differs("created_at", created), differs("sender_id", literal(p.Sender)),
	}}}}}}
	keep := func(cond bson.D, field string, next any) bson.E {
		return bson.E{Key: field, Value: bson.D{{Key: "$cond", Value: bson.A{cond, next, "$" + field}}}}
	}
	set := bson.D{
		keep(writable, "message_key", literal(at.key)),
		keep(writable, "room_id", at.room),
		keep(writable, "tenant", literal(p.Tenant)),
		keep(writable, "kind", literal(targetKindNames[t.Kind])),
		keep(writable, "target", literal(t.Name(p.Key.Room))),
		keep(writable, "thread_root", at.thread),
		keep(writable, "seq", at.seq),
		keep(writable, "sender_id", literal(p.Sender)),
		keep(writable, "state", int64(state)),
		keep(writable, "message_ver", ver),
		keep(writable, "created_at", created),
		keep(changed, "updated_at", now),
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func decodeMention(d mentionDoc) (domain.Mention, error) {
	msgKey, kind, id, err := keys.ParseMention(d.ID)
	if err != nil {
		return domain.Mention{}, fmt.Errorf("%w: mention _id: %w", errCorrupt, err)
	}
	room, thread, seq, _ := keys.ParseMsg(msgKey)
	ver, err := narrowUint32("mention message_ver", d.MessageVer)
	if err != nil {
		return domain.Mention{}, err
	}
	state, ok := mentionStates[d.State]
	if !ok {
		return domain.Mention{}, fmt.Errorf("%w: mention state %d", errCorrupt, d.State)
	}
	return domain.Mention{
		Key: domain.MessageKey{Room: room, Thread: thread, Seq: seq}, Tenant: d.Tenant,
		Target: domain.Target{Kind: domain.TargetKind(kind), ID: id}, Sender: d.Sender, State: state,
		MessageVer: ver, CreatedAt: d.CreatedAt.UTC(), UpdatedAt: d.UpdatedAt.UTC(),
	}, nil
}

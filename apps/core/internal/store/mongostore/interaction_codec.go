package mongostore

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const (
	interactionLive    int64 = 1
	interactionRemoved int64 = 2
)

var interactionKindNames = map[keys.InteractionKind]string{
	keys.ReactionKind: "reaction",
	keys.BookmarkKind: "bookmark",
	keys.ReplyKind:    "reply",
}

type interactionDoc struct {
	ID        []byte    `bson:"_id"`
	Key       []byte    `bson:"message_key"`
	Room      int64     `bson:"room_id"`
	Tenant    string    `bson:"tenant"`
	Kind      string    `bson:"kind"`
	Actor     string    `bson:"actor_id"`
	Value     string    `bson:"value"`
	Prev      string    `bson:"previous_value"`
	ReplySeq  int64     `bson:"reply_seq"`
	State     int64     `bson:"state"`
	Ver       int64     `bson:"ver"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

type interactionHead struct {
	kind  keys.InteractionKind
	key   store.MsgKey
	user  string
	reply store.MsgKey
	live  bool
	ver   uint32
}

func decodeHead(d interactionDoc, want keys.InteractionKind) (interactionHead, error) {
	msgKey, kind, part, err := keys.ParseInteraction(d.ID)
	if err != nil {
		return interactionHead{}, fmt.Errorf("%w: interaction _id: %w", errCorrupt, err)
	}
	if want != 0 && kind != want {
		return interactionHead{}, fmt.Errorf("%w: interaction kind %d, want %d", errCorrupt, kind, want)
	}
	room, thread, seq, _ := keys.ParseMsg(msgKey)
	h := interactionHead{kind: kind, key: store.MsgKey{Room: room, Thread: thread, Seq: seq}}
	if kind == keys.ReplyKind {
		replyThread, replySeq, _ := keys.ParseReplyPart(part)
		h.reply, h.user = store.MsgKey{Room: room, Thread: replyThread, Seq: replySeq}, d.Actor
	} else {
		h.user = string(part)
	}
	if err := domain.ValidUser(h.user); err != nil {
		return interactionHead{}, fmt.Errorf("%w: interaction user: %w", errCorrupt, err)
	}
	if h.ver, err = narrowUint32("interaction ver", d.Ver); err != nil {
		return interactionHead{}, err
	}
	switch d.State {
	case interactionLive:
		h.live = true
	case interactionRemoved:
	default:
		return interactionHead{}, fmt.Errorf("%w: interaction state %d", errCorrupt, d.State)
	}
	return h, nil
}

func decodeReaction(d interactionDoc) (domain.Reaction, error) {
	h, err := decodeHead(d, keys.ReactionKind)
	if err != nil {
		return domain.Reaction{}, err
	}
	r := domain.Reaction{
		Room: h.key.Room, Thread: h.key.Thread, Seq: h.key.Seq, Tenant: d.Tenant, User: h.user,
		Emoji: d.Value, Prev: d.Prev, N: h.ver, At: d.UpdatedAt,
	}
	if !h.live {
		r.Emoji, r.Prev = "", d.Value
	}
	return r, nil
}

func decodeBookmark(d interactionDoc) (domain.Bookmark, error) {
	h, err := decodeHead(d, keys.BookmarkKind)
	if err != nil {
		return domain.Bookmark{}, err
	}
	return domain.Bookmark{
		Room: h.key.Room, Thread: h.key.Thread, Seq: h.key.Seq, Tenant: d.Tenant, User: h.user, On: h.live, Ver: h.ver, At: d.UpdatedAt,
	}, nil
}

func decodeReply(d interactionDoc) (domain.Reply, error) {
	h, err := decodeHead(d, keys.ReplyKind)
	if err != nil {
		return domain.Reply{}, err
	}
	return domain.Reply{
		Parent: domain.MsgKey(h.key), Room: h.reply.Room, Thread: h.reply.Thread, Seq: h.reply.Seq,
		Tenant: d.Tenant, From: h.user, Live: h.live, Ver: h.ver, At: d.UpdatedAt,
	}, nil
}

func decodeInteraction(d interactionDoc) (store.Interaction, error) {
	h, err := decodeHead(d, 0)
	if err != nil {
		return store.Interaction{}, err
	}
	return store.Interaction{Kind: h.kind, Key: h.key, User: h.user, Ver: h.ver, At: d.UpdatedAt, Reply: h.reply}, nil
}

func decodeAll[T any](docs []interactionDoc, decode func(interactionDoc) (T, error)) ([]T, error) {
	out := make([]T, len(docs))
	for i, d := range docs {
		x, err := decode(d)
		if err != nil {
			return nil, err
		}
		out[i] = x
	}
	return out, nil
}

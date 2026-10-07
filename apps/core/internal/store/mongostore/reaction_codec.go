package mongostore

import (
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type reactionDoc struct {
	ID     []byte    `bson:"_id"`
	Key    []byte    `bson:"message_key"`
	Room   int64     `bson:"room_id"`
	Tenant string    `bson:"tenant"`
	User   string    `bson:"user_id"`
	Prev   string    `bson:"previous_emoji"`
	Emoji  string    `bson:"emoji"`
	N      int64     `bson:"ver"`
	At     time.Time `bson:"updated_at"`
}

type reactionsDoc struct {
	Counts  []countDoc `bson:"c"`
	Version int64      `bson:"v"`
}

type countDoc struct {
	Emoji string `bson:"e"`
	N     int64  `bson:"n"`
}

func decodeReaction(d reactionDoc) (domain.Reaction, error) {
	room, thread, seq, user, err := keys.ParseReaction(d.ID)
	if err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction _id: %w", errCorrupt, err)
	}
	if err := domain.ValidUser(user); err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction user: %w", errCorrupt, err)
	}
	n, err := narrowUint32("reaction change", d.N)
	if err != nil {
		return domain.Reaction{}, err
	}
	return domain.Reaction{
		Room: room, Thread: thread, Seq: seq, Tenant: d.Tenant, User: user, Emoji: d.Emoji, Prev: d.Prev, N: n, At: d.At,
	}, nil
}

func decodeReactions(docs []reactionDoc) ([]domain.Reaction, error) {
	out := make([]domain.Reaction, len(docs))
	for i, d := range docs {
		r, err := decodeReaction(d)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

func encodeSummary(s domain.ReactionSummary) (reactionsDoc, error) {
	v, err := toInt64("reactions version", s.Version)
	if err != nil {
		return reactionsDoc{}, err
	}
	counts := make([]countDoc, len(s.Counts))
	for i, c := range s.Counts {
		counts[i] = countDoc{Emoji: c.Emoji, N: int64(c.Count)}
	}
	return reactionsDoc{Counts: counts, Version: v}, nil
}

func decodeSummary(d *reactionsDoc) (domain.ReactionSummary, error) {
	if d == nil {
		return domain.ReactionSummary{}, nil
	}
	v, err := toUint64("reactions version", d.Version)
	if err != nil {
		return domain.ReactionSummary{}, err
	}
	counts := make([]domain.ReactionCount, len(d.Counts))
	for i, c := range d.Counts {
		n, err := narrowUint32("reaction count", c.N)
		if err != nil {
			return domain.ReactionSummary{}, err
		}
		counts[i] = domain.ReactionCount{Emoji: c.Emoji, Count: n}
	}
	return domain.ReactionSummary{Counts: counts, Version: v}, nil
}

func narrowUint32(field string, v int64) (uint32, error) {
	if v < 0 || v > math.MaxUint32 {
		return 0, fmt.Errorf("%w: %s %d out of range", errCorrupt, field, v)
	}
	return uint32(v), nil
}

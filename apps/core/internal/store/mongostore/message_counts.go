package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MessageCounts = (*Store)(nil)

type countsDoc struct {
	Reactions *reactionsDoc  `bson:"rx"`
	Replies   *replyCountDoc `bson:"rc"`
}

func (s *Store) AddReactionCounts(ctx context.Context, key store.MsgKey, deltas []store.EmojiDelta) (domain.ReactionSummary, error) {
	if err := key.Validate(); err != nil {
		return domain.ReactionSummary{}, err
	}
	if err := store.ValidateEmojiDeltas(deltas); err != nil {
		return domain.ReactionSummary{}, err
	}
	d, err := s.addCounts(ctx, key, reactionDeltaPipeline(deltas), "rx")
	if err != nil {
		return domain.ReactionSummary{}, fmt.Errorf("add reaction counts to %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	return decodeSummary(d.Reactions)
}

func (s *Store) AddReplyCount(ctx context.Context, key store.MsgKey, delta int) (domain.ReplyCount, error) {
	if err := key.Validate(); err != nil {
		return domain.ReplyCount{}, err
	}
	if err := store.ValidateCountDelta(delta); err != nil {
		return domain.ReplyCount{}, err
	}
	d, err := s.addCounts(ctx, key, replyDeltaPipeline(delta), "rc")
	switch {
	case err != nil:
		return domain.ReplyCount{}, fmt.Errorf("add %d to reply count of %d/%d/%d: %w", delta, key.Room, key.Thread, key.Seq, err)
	case d.Replies == nil:
		return domain.ReplyCount{}, fmt.Errorf("%w: reply count missing after an update", errCorrupt)
	}
	return decodeReplyCount(*d.Replies)
}

func (s *Store) SetReplyCount(ctx context.Context, key store.MsgKey, base uint64, n uint32) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, base+1); err != nil {
		return false, err
	}
	from, err := toInt64("reply count base version", base)
	if err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: msgID(key)}, versionIs("rc.v", from)}
	count := bson.D{{Key: "n", Value: int64(n)}, {Key: "v", Value: from + 1}}
	res, err := s.messages.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "rc", Value: count}}}})
	if err != nil {
		return false, fmt.Errorf("set reply count v%d of %d/%d/%d: %w", base+1, key.Room, key.Thread, key.Seq, err)
	}
	return res.MatchedCount == 1, nil
}

func (s *Store) addCounts(ctx context.Context, key store.MsgKey, update mongo.Pipeline, field string) (countsDoc, error) {
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(bson.D{{Key: field, Value: 1}})
	var d countsDoc
	err := s.messages.FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: msgID(key)}}, update, opts).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = domain.ErrMessageNotFound
	}
	return d, err
}

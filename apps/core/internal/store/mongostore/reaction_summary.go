package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Store) SetReactions(ctx context.Context, key store.MsgKey, base uint64, sum domain.ReactionSummary) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, sum.Version); err != nil {
		return false, err
	}
	doc, err := encodeSummary(sum)
	if err != nil {
		return false, err
	}
	from, err := toInt64("reactions base version", base)
	if err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: keys.Msg(key.Room, key.Thread, key.Seq)}, versionIs("rx.v", from)}
	res, err := s.messages.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "rx", Value: doc}}}})
	if err != nil {
		return false, fmt.Errorf("set reactions v%d of %d/%d/%d: %w", sum.Version, key.Room, key.Thread, key.Seq, err)
	}
	return res.MatchedCount == 1, nil
}

func versionIs(field string, base int64) bson.E {
	if base == 0 {
		return bson.E{Key: field, Value: bson.D{{Key: "$exists", Value: false}}}
	}
	return bson.E{Key: field, Value: base}
}

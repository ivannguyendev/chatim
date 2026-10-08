package mongostore

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Store) Append(ctx context.Context, e domain.Edit) error {
	doc, err := encodeEdit(e)
	if err != nil {
		return err
	}
	if _, err := s.edits.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("append edit v%d of %d/%d/%d: %w", e.Version, e.Room, e.Thread, e.Seq, store.ErrEditExists)
		}
		return fmt.Errorf("append edit v%d of %d/%d/%d: %w", e.Version, e.Room, e.Thread, e.Seq, err)
	}
	return nil
}

func (s *Store) At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error) {
	var d editDoc
	filter := bson.D{{Key: "_id", Value: keys.Edit(key.Room, key.Thread, key.Seq, version)}}
	if err := findOne(ctx, s.edits, filter, &d, store.ErrEditNotFound); err != nil {
		return domain.Edit{}, fmt.Errorf("edit v%d of %d/%d/%d: %w", version, key.Room, key.Thread, key.Seq, err)
	}
	return decodeEdit(d)
}

func (s *Store) Latest(ctx context.Context, key store.MsgKey) (domain.Edit, bool, error) {
	got, err := s.findEdits(ctx, versionsAfter(key, 0), bson.D{{Key: "_id", Value: -1}}, 1)
	if err != nil || len(got) == 0 {
		return domain.Edit{}, false, err
	}
	return got[0], true, nil
}

func (s *Store) History(ctx context.Context, key store.MsgKey, after uint32, limit int) ([]domain.Edit, error) {
	if err := store.ValidateLimit(limit, store.MaxEditPage); err != nil {
		return nil, err
	}
	return s.findEdits(ctx, versionsAfter(key, after), bson.D{{Key: "_id", Value: 1}}, limit)
}

func (s *Store) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error) {
	if err := store.ValidateLimit(limit, store.MaxEditScan); err != nil {
		return nil, err
	}
	r, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "room_id", Value: r}, {Key: "created_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return s.findEdits(ctx, filter, bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}, limit)
}

func (s *Store) PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error {
	filter := bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gte", Value: keys.Edit(key.Room, key.Thread, key.Seq, 0)},
		{Key: "$lte", Value: keys.Edit(key.Room, key.Thread, key.Seq, upTo)},
	}}}
	update := bson.D{{Key: "$unset", Value: bson.D{{Key: "text", Value: ""}}}}
	if _, err := s.edits.UpdateMany(ctx, filter, update); err != nil {
		return fmt.Errorf("purge text of %d/%d/%d up to v%d: %w", key.Room, key.Thread, key.Seq, upTo, err)
	}
	return nil
}

func versionsAfter(key store.MsgKey, after uint32) bson.D {
	return bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gt", Value: keys.Edit(key.Room, key.Thread, key.Seq, after)},
		{Key: "$lte", Value: keys.Edit(key.Room, key.Thread, key.Seq, math.MaxUint32)},
	}}}
}

func (s *Store) findEdits(ctx context.Context, filter, sort bson.D, limit int) ([]domain.Edit, error) {
	cur, err := s.edits.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("find edits: %w", err)
	}
	var docs []editDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("find edits: %w", err)
	}
	return decodeEdits(docs)
}

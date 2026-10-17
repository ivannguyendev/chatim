package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

var _ store.Rooms = (*Rooms)(nil)

type Rooms struct {
	coll *mongo.Collection
}

func NewRooms(db *mongo.Database) *Rooms {
	return &Rooms{coll: db.Collection(roomsCollection)}
}

type roomDoc struct {
	ID int64     `bson:"_id"`
	At time.Time `bson:"last_shared_mention_at"`
}

func (s *Rooms) Raise(ctx context.Context, tenant string, room uint64, at time.Time) error {
	if err := store.ValidateRaise(tenant, room, at); err != nil {
		return err
	}
	id, err := toInt64("room id", room)
	if err != nil {
		return err
	}
	_, err = s.coll.UpdateOne(ctx, bson.D{{Key: "_id", Value: id}}, raisePipeline(tenant, at.UTC()), options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("raise shared mention of room %d: %w", room, err)
	}
	return nil
}

func raisePipeline(tenant string, at time.Time) bson.A {
	sameTenant := bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$tenant", tenant}}}, tenant}}}
	raised := bson.D{{Key: "$max", Value: bson.A{"$last_shared_mention_at", at}}}
	set := bson.D{
		{Key: "tenant", Value: bson.D{{Key: "$cond", Value: bson.A{sameTenant, literal(tenant), "$tenant"}}}},
		{Key: "last_shared_mention_at", Value: bson.D{{Key: "$cond", Value: bson.A{sameTenant, raised, "$last_shared_mention_at"}}}},
	}
	return bson.A{bson.D{{Key: "$set", Value: set}}}
}

func (s *Rooms) Times(ctx context.Context, tenant string, rooms []uint64) (map[uint64]time.Time, error) {
	if err := store.ValidateRooms(tenant, rooms); err != nil {
		return nil, err
	}
	out := make(map[uint64]time.Time)
	for start := 0; start < len(rooms); start += store.SharedMentionChunk {
		if err := s.timesChunk(ctx, tenant, rooms[start:min(len(rooms), start+store.SharedMentionChunk)], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Rooms) timesChunk(ctx context.Context, tenant string, rooms []uint64, out map[uint64]time.Time) error {
	ids := make(bson.A, len(rooms))
	for i, r := range rooms {
		id, err := toInt64("room id", r)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}, {Key: "tenant", Value: tenant}}
	opts := options.Find().SetProjection(bson.D{{Key: "last_shared_mention_at", Value: 1}})
	docs, err := findAll[roomDoc](ctx, s.coll, filter, opts)
	if err != nil {
		return fmt.Errorf("shared mention times of %d rooms: %w", len(rooms), err)
	}
	for _, d := range docs {
		room, err := toUint64("room id", d.ID)
		if err != nil {
			return err
		}
		out[room] = d.At.UTC()
	}
	return nil
}

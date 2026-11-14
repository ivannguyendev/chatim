package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

var _ store.RoomAvatars = (*RoomAvatars)(nil)

type RoomAvatars struct {
	coll *mongo.Collection
}

type roomAvatarDoc struct {
	Room      string    `bson:"_id"`
	Tenant    string    `bson:"tenant_id"`
	FileKey   string    `bson:"file_key,omitempty"`
	InfoVer   uint64    `bson:"info_ver"`
	UpdatedAt time.Time `bson:"updated_at"`
}

func NewRoomAvatars(db *mongo.Database) *RoomAvatars {
	return &RoomAvatars{coll: db.Collection(roomAvatarsCollection)}
}

func (s *RoomAvatars) Get(ctx context.Context, room string) (domain.RoomAvatar, bool, error) {
	var d roomAvatarDoc
	err := s.coll.FindOne(ctx, bson.D{{Key: "_id", Value: room}}).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return domain.RoomAvatar{}, false, nil
	case err != nil:
		return domain.RoomAvatar{}, false, fmt.Errorf("get room avatar %s: %w", room, err)
	}
	return domain.RoomAvatar{Room: d.Room, Tenant: d.Tenant, FileKey: d.FileKey, InfoVer: d.InfoVer, UpdatedAt: d.UpdatedAt.UTC()}, true, nil
}

func (s *RoomAvatars) Put(ctx context.Context, next domain.RoomAvatar, prevVer uint64) error {
	prev := bson.E{Key: "info_ver", Value: prevVer}
	if prevVer == 0 {
		prev = bson.E{Key: "info_ver", Value: bson.D{{Key: "$exists", Value: false}}}
	}
	doc := roomAvatarDoc{
		Room: next.Room, Tenant: next.Tenant, FileKey: next.FileKey, InfoVer: next.InfoVer,
		UpdatedAt: next.UpdatedAt.Truncate(time.Millisecond).UTC(),
	}
	_, err := s.coll.ReplaceOne(ctx, bson.D{{Key: "_id", Value: next.Room}, prev}, doc, options.Replace().SetUpsert(true))
	switch {
	case hasCode(err, duplicateKeyCode):
		return domain.ErrRetryLater
	case err != nil:
		return fmt.Errorf("put room avatar %s: %w", next.Room, err)
	}
	return nil
}

func (s *RoomAvatars) InUse(ctx context.Context, fileKey string) (bool, error) {
	opts := options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}}).SetHint(avatarsByFile)
	err := s.coll.FindOne(ctx, bson.D{{Key: "file_key", Value: fileKey}}, opts).Err()
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("room avatar file in use: %w", err)
	}
	return true, nil
}

package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Store) PinState(ctx context.Context, room uint64) (domain.PinState, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.PinState{}, fmt.Errorf("pins of room %d: %w", room, domain.ErrRoomNotFound)
	}
	var d pinStateDoc
	onlyPins := options.FindOne().SetProjection(bson.D{{Key: "pins", Value: 1}, {Key: "pin_ver", Value: 1}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, onlyPins); err != nil {
		return domain.PinState{}, fmt.Errorf("pins of room %d: %w", room, err)
	}
	return decodePinState(d)
}

func (s *Store) ApplyPins(ctx context.Context, room, base uint64, st domain.PinState) (bool, error) {
	if err := store.ValidateVersionBump(base, st.Version); err != nil {
		return false, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return false, err
	}
	from, err := toInt64("pin base version", base)
	if err != nil {
		return false, err
	}
	pins, pv, err := encodePinState(st)
	if err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("pin_ver", from)}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "pins", Value: pins}, {Key: "pin_ver", Value: pv}}}}
	res, err := s.rooms.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("apply pins v%d to room %d: %w", st.Version, room, err)
	}
	return res.MatchedCount == 1, nil
}

package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var readPositionFields = bson.D{{Key: "read_seq", Value: 1}, {Key: "read_ver", Value: 1}}

func (s *Store) MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, seq, "$lt")
}

func (s *Store) MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, to, "$gt")
}

func (s *Store) moveRead(ctx context.Context, room uint64, user string, seq uint64, moves string) (domain.ReadPosition, bool, error) {
	if err := store.ValidateReadSeq(seq); err != nil {
		return domain.ReadPosition{}, false, err
	}
	to, err := toInt64("read seq", seq)
	if err != nil {
		return domain.ReadPosition{}, false, err
	}
	filter := append(activeMemberFilter(room, user), bson.E{Key: "read_seq", Value: bson.D{{Key: moves, Value: to}}})
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(readPositionFields)
	var d readPositionDoc
	err = s.members.FindOneAndUpdate(ctx, filter, readMoveUpdate(to, time.Now()), opts).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		pos, err := s.readPosition(ctx, room, user)
		return pos, false, err
	case err != nil:
		return domain.ReadPosition{}, false, fmt.Errorf("move read position of %q in room %d to %d: %w", user, room, seq, err)
	}
	pos, err := decodeReadPosition(d)
	return pos, err == nil, err
}

func readMoveUpdate(to int64, now time.Time) mongo.Pipeline {
	return flaggedSet(store.BitReadPosition,
		bson.E{Key: "read_seq", Value: to},
		bson.E{Key: "read_ver", Value: bumped("$read_ver")},
		latest("last_change_at", time.UnixMilli(now.UnixMilli()).UTC()),
	)
}

func (s *Store) readPosition(ctx context.Context, room uint64, user string) (domain.ReadPosition, error) {
	var d readPositionDoc
	onlyRead := options.FindOne().SetProjection(readPositionFields)
	if err := findOne(ctx, s.members, activeMemberFilter(room, user), &d, domain.ErrNotMember, onlyRead); err != nil {
		return domain.ReadPosition{}, fmt.Errorf("read position of %q in room %d: %w", user, room, err)
	}
	return decodeReadPosition(d)
}

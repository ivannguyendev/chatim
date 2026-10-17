package mongostore

import (
	"context"
	"fmt"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var hiddenRoomFields = bson.D{{Key: "room_id", Value: 1}, {Key: "seq", Value: 1}, {Key: "_id", Value: 0}}

func (h *Hidden) HiddenInRooms(ctx context.Context, user string, ranges []store.HiddenRange) (map[uint64][]uint64, error) {
	if err := store.ValidateHiddenRanges(user, ranges); err != nil {
		return nil, err
	}
	out := map[uint64][]uint64{}
	if len(ranges) == 0 {
		return out, ctx.Err()
	}
	filter, err := hiddenRoomsFilter(user, ranges)
	if err != nil {
		return nil, err
	}
	docs, err := h.find(ctx, filter, options.Find().SetProjection(hiddenRoomFields))
	if err != nil {
		return nil, fmt.Errorf("hidden of %q in %d rooms: %w", user, len(ranges), err)
	}
	for _, d := range docs {
		room, roomErr := toUint64("hidden room", d.Room)
		seq, seqErr := toUint64("hidden seq", d.Seq)
		if err := firstErr(roomErr, seqErr); err != nil {
			return nil, err
		}
		out[room] = append(out[room], seq)
	}
	for _, seqs := range out {
		slices.Sort(seqs)
	}
	return out, nil
}

func hiddenRoomsFilter(user string, ranges []store.HiddenRange) (bson.D, error) {
	branches := make(bson.A, len(ranges))
	for i, r := range ranges {
		room, roomErr := toInt64("room id", r.Room)
		from, fromErr := toInt64("from", r.From)
		to, toErr := toInt64("to", r.To)
		if err := firstErr(roomErr, fromErr, toErr); err != nil {
			return nil, err
		}
		branches[i] = bson.D{
			{Key: "user_id", Value: user}, {Key: "room_id", Value: room}, {Key: "thread_root", Value: int64(0)},
			{Key: "seq", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}},
		}
	}
	return bson.D{{Key: "$or", Value: branches}}, nil
}

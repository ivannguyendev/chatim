package mongostore

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type pendingRow struct {
	ID      bson.RawValue `bson:"_id"`
	State   int64         `bson:"_state"`
	StateAt time.Time     `bson:"_state_at"`
	User    string        `bson:"user_id,omitempty"`
	Room    int64         `bson:"room_id,omitempty"`
	Thread  int64         `bson:"thread_root,omitempty"`
	Seq     int64         `bson:"seq,omitempty"`
}

var pendingSort = bson.D{{Key: stateAtField, Value: 1}, {Key: "_id", Value: 1}}

func pendingProjection(c store.StateCollection) bson.D {
	p := bson.D{{Key: "_id", Value: 1}, {Key: stateField, Value: 1}, {Key: stateAtField, Value: 1}}
	if c == store.StateHidden {
		p = append(p, bson.E{Key: "user_id", Value: 1}, bson.E{Key: "room_id", Value: 1}, bson.E{Key: "thread_root", Value: 1}, bson.E{Key: "seq", Value: 1})
	}
	return p
}

func pendingFilter(c store.StateCollection, from, before time.Time, after store.PendingCursor) (bson.D, bool, error) {
	from, before = toMillis(from), toMillis(before)
	at := toMillis(after.At)
	if after.IsZero() || at.Before(from) {
		within := bson.D{{Key: "$gte", Value: from}, {Key: "$lt", Value: before}}
		return bson.D{{Key: stateAtField, Value: within}, stateExists}, from.Before(before), nil
	}
	if !at.Before(before) {
		return nil, false, nil
	}
	id, err := cursorID(c, after.Key)
	if err != nil {
		return nil, false, err
	}
	tie := bson.D{{Key: stateAtField, Value: at}, {Key: "_id", Value: bson.D{{Key: "$gt", Value: id}}}, stateExists}
	later := bson.D{{Key: stateAtField, Value: bson.D{{Key: "$gt", Value: at}, {Key: "$lt", Value: before}}}, stateExists}
	return bson.D{{Key: "$or", Value: bson.A{tie, later}}}, true, nil
}

func cursorID(c store.StateCollection, key []byte) (any, error) {
	switch c {
	case store.StateRooms:
		if len(key) != 8 {
			return nil, fmt.Errorf("%w: pending cursor key", apperr.ErrInvalidArgument)
		}
		id, ok := int64Of(binary.BigEndian.Uint64(key))
		if !ok {
			return nil, fmt.Errorf("%w: pending cursor key", apperr.ErrInvalidArgument)
		}
		return id, nil
	case store.StateHidden:
		var oid bson.ObjectID
		if len(key) != len(oid) {
			return nil, fmt.Errorf("%w: pending cursor key", apperr.ErrInvalidArgument)
		}
		copy(oid[:], key)
		return oid, nil
	default:
		return key, nil
	}
}

func decodePending(c store.StateCollection, row pendingRow) (store.PendingDoc, []byte, error) {
	if row.State <= 0 || row.State > math.MaxUint32 {
		return store.PendingDoc{}, nil, fmt.Errorf("%w: %s %s %d", errCorrupt, c, stateField, row.State)
	}
	d := store.PendingDoc{Collection: c, State: store.StateBit(row.State), StateAt: row.StateAt.UTC()}
	key, err := decodePendingKey(c, row, &d)
	if err != nil {
		return store.PendingDoc{}, nil, fmt.Errorf("%w: %s pending _id: %w", errCorrupt, c, err)
	}
	return d, key, nil
}

func decodePendingKey(c store.StateCollection, row pendingRow, d *store.PendingDoc) ([]byte, error) {
	switch c {
	case store.StateRooms:
		id, ok := row.ID.Int64OK()
		if !ok || id <= 0 {
			return nil, fmt.Errorf("room id %s", row.ID)
		}
		d.Room = uint64(id)
		return binary.BigEndian.AppendUint64(nil, d.Room), nil
	case store.StateHidden:
		oid, ok := row.ID.ObjectIDOK()
		if !ok || row.Room <= 0 || row.Thread < 0 || row.Seq <= 0 || row.User == "" {
			return nil, fmt.Errorf("hidden %s", row.ID)
		}
		d.Room, d.Thread, d.Seq, d.User = uint64(row.Room), uint64(row.Thread), uint64(row.Seq), row.User
		return oid[:], nil
	default:
		_, key, ok := row.ID.BinaryOK()
		if !ok {
			return nil, fmt.Errorf("not binary: %s", row.ID)
		}
		return key, decodeBinaryKey(c, key, d)
	}
}

func decodeBinaryKey(c store.StateCollection, key []byte, d *store.PendingDoc) error {
	var err error
	switch c {
	case store.StateEdits:
		var ver uint32
		d.Room, d.Thread, d.Seq, ver, err = keys.ParseEdit(key)
		d.Ver = uint64(ver)
	case store.StateInteractions:
		return decodeInteractionKey(key, d)
	case store.StatePins:
		d.Room, d.Seq, err = keys.ParsePin(key)
	case store.StateMembers:
		d.Room, d.User, err = keys.ParseMember(key)
	default:
		d.Room, d.Thread, d.Seq, err = keys.ParseMsg(key)
	}
	return err
}

func decodeInteractionKey(key []byte, d *store.PendingDoc) error {
	msg, kind, part, err := keys.ParseInteraction(key)
	if err != nil {
		return err
	}
	if kind != keys.ReactionKind && kind != keys.BookmarkKind {
		return fmt.Errorf("interaction kind %d", kind)
	}
	d.Room, d.Thread, d.Seq, err = keys.ParseMsg(msg)
	d.User, d.Kind = string(part), kind
	return err
}

func toMillis(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC().Truncate(time.Millisecond)
}

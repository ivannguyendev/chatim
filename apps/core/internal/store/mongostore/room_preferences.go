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

type preferenceDoc struct {
	Ver        int64     `bson:"preference_ver"`
	PinnedAt   time.Time `bson:"pinned_at,omitempty"`
	MutedUntil time.Time `bson:"muted_until,omitempty"`
	ArchivedAt time.Time `bson:"archived_at,omitempty"`
}

type preferenceField struct {
	name  string
	value time.Time
	exact bool
}

var preferenceFields = bson.D{{Key: "preference_ver", Value: 1}, {Key: "pinned_at", Value: 1}, {Key: "muted_until", Value: 1}, {Key: "archived_at", Value: 1}}

func (s *Store) SetPreferences(ctx context.Context, room uint64, user string, c domain.PreferencesChange, at time.Time) (domain.RoomPreferences, bool, error) {
	if err := store.ValidatePreferences(room, user, c, at); err != nil {
		return domain.RoomPreferences{}, false, err
	}
	at = time.UnixMilli(at.UnixMilli()).UTC()
	fields := preferenceTargets(c, at)
	filter := append(activeMemberFilter(room, user), bson.E{Key: "$or", Value: preferenceDiffers(fields)})
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(preferenceFields)
	var d preferenceDoc
	err := s.members.FindOneAndUpdate(ctx, filter, preferencePipeline(fields, at), opts).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		cur, err := s.preferences(ctx, room, user)
		return cur, false, err
	case err != nil:
		return domain.RoomPreferences{}, false, fmt.Errorf("set preferences of %q in room %d: %w", user, room, err)
	}
	got, err := decodePreferences(d)
	return got, err == nil, err
}

func preferenceTargets(c domain.PreferencesChange, at time.Time) []preferenceField {
	next, _ := c.Apply(domain.RoomPreferences{}, at)
	var out []preferenceField
	if c.Pinned != nil {
		out = append(out, preferenceField{name: "pinned_at", value: next.PinnedAt})
	}
	if c.MutedUntil != nil {
		out = append(out, preferenceField{name: "muted_until", value: next.MutedUntil, exact: true})
	}
	if c.Archived != nil {
		out = append(out, preferenceField{name: "archived_at", value: next.ArchivedAt})
	}
	return out
}

func preferenceDiffers(fields []preferenceField) bson.A {
	out := make(bson.A, len(fields))
	for i, f := range fields {
		switch {
		case f.value.IsZero():
			out[i] = bson.D{{Key: f.name, Value: bson.D{{Key: "$exists", Value: true}}}}
		case f.exact:
			out[i] = bson.D{{Key: f.name, Value: bson.D{{Key: "$ne", Value: f.value}}}}
		default:
			out[i] = bson.D{{Key: f.name, Value: bson.D{{Key: "$exists", Value: false}}}}
		}
	}
	return out
}

func preferencePipeline(fields []preferenceField, at time.Time) mongo.Pipeline {
	set := bson.D{}
	for _, f := range fields {
		switch {
		case f.value.IsZero():
			set = append(set, bson.E{Key: f.name, Value: "$$REMOVE"})
		case f.exact:
			set = append(set, bson.E{Key: f.name, Value: f.value})
		default:
			set = append(set, bson.E{Key: f.name, Value: bson.D{{Key: "$ifNull", Value: bson.A{"$" + f.name, f.value}}}})
		}
	}
	set = append(set,
		bson.E{Key: "preference_ver", Value: bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$preference_ver", int64(0)}}}, int64(1)}}}},
		bson.E{Key: "last_change_at", Value: bson.D{{Key: "$max", Value: bson.A{"$last_change_at", at}}}},
	)
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func (s *Store) preferences(ctx context.Context, room uint64, user string) (domain.RoomPreferences, error) {
	var d preferenceDoc
	only := options.FindOne().SetProjection(preferenceFields)
	if err := findOne(ctx, s.members, activeMemberFilter(room, user), &d, domain.ErrNotMember, only); err != nil {
		return domain.RoomPreferences{}, fmt.Errorf("preferences of %q in room %d: %w", user, room, err)
	}
	return decodePreferences(d)
}

func decodePreferences(d preferenceDoc) (domain.RoomPreferences, error) {
	ver, err := toUint64("member preference ver", d.Ver)
	if err != nil {
		return domain.RoomPreferences{}, err
	}
	return domain.RoomPreferences{PinnedAt: d.PinnedAt, MutedUntil: d.MutedUntil, ArchivedAt: d.ArchivedAt, Ver: ver}, nil
}

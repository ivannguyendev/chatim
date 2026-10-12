package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Store) ApplyEdit(ctx context.Context, e domain.Edit) error {
	if err := store.ValidateProjectedEdit(e); err != nil {
		return err
	}
	version, err := toInt32("version", e.Version)
	if err != nil {
		return err
	}
	filter := bson.D{
		{Key: "_id", Value: keys.Msg(e.Room, e.Thread, e.Seq)},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "v", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "v", Value: bson.D{{Key: "$lt", Value: version}}}},
		}},
	}
	update, err := editUpdate(e, version)
	if err != nil {
		return err
	}
	if _, err := s.messages.UpdateOne(ctx, filter, update); err != nil {
		return fmt.Errorf("apply edit v%d to %d/%d/%d: %w", e.Version, e.Room, e.Thread, e.Seq, err)
	}
	return nil
}

func editUpdate(e domain.Edit, version int32) (bson.D, error) {
	if e.Kind == domain.EditDelete {
		set := bson.D{{Key: "v", Value: version}, {Key: "ea", Value: e.At}, {Key: "x", Value: ""}, {Key: "d", Value: true}}
		unset := bson.D{{Key: "mt", Value: ""}, {Key: "ma", Value: ""}, {Key: "fw", Value: ""}}
		return bson.D{{Key: "$set", Value: set}, {Key: "$unset", Value: unset}}, nil
	}
	mentions, err := encodeMessageMentions(e.Mentions)
	if err != nil {
		return nil, err
	}
	set := bson.D{{Key: "v", Value: version}, {Key: "ea", Value: e.At}, {Key: "x", Value: e.Text}, {Key: "d", Value: false}}
	var unset bson.D
	if len(mentions) > 0 {
		set = append(set, bson.E{Key: "mt", Value: mentions})
	} else {
		unset = append(unset, bson.E{Key: "mt", Value: ""})
	}
	if e.MentionAll {
		set = append(set, bson.E{Key: "ma", Value: true})
	} else {
		unset = append(unset, bson.E{Key: "ma", Value: ""})
	}
	update := bson.D{{Key: "$set", Value: set}}
	if len(unset) > 0 {
		update = append(update, bson.E{Key: "$unset", Value: unset})
	}
	return update, nil
}

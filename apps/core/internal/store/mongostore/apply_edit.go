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
	text, deleted := e.Text, e.Kind == domain.EditDelete
	if deleted {
		text = ""
	}
	set := bson.D{{Key: "v", Value: version}, {Key: "ea", Value: e.At}, {Key: "x", Value: text}, {Key: "d", Value: deleted}}
	update := bson.D{{Key: "$set", Value: set}}
	if deleted {
		update = append(update, bson.E{Key: "$unset", Value: bson.D{{Key: "mt", Value: ""}, {Key: "ma", Value: ""}, {Key: "fw", Value: ""}}})
	}
	if _, err := s.messages.UpdateOne(ctx, filter, update); err != nil {
		return fmt.Errorf("apply edit v%d to %d/%d/%d: %w", e.Version, e.Room, e.Thread, e.Seq, err)
	}
	return nil
}

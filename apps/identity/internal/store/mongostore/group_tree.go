package mongostore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var errGroupCycle = fmt.Errorf("%w: group cycle", apperr.ErrFailedPrecondition)

func (s *Store) UpdateGroup(ctx context.Context, tenantID, id string, u store.GroupUpdate, at time.Time, by string) error {
	if u.ParentID == nil {
		return s.mapErr(s.updateLiveGroup(ctx, tenantID, id, visibleSet(groupFields(u), at, by)))
	}
	parentID := *u.ParentID
	return s.runOnce(ctx, func(ctx context.Context) error {
		if err := s.bumpGroupTree(ctx, tenantID); err != nil {
			return err
		}
		if _, err := s.liveGroup(ctx, tenantID, id); err != nil {
			return err
		}
		ancestors, err := s.pathUnder(ctx, tenantID, parentID)
		if err != nil {
			return err
		}
		if slices.Contains(ancestors, id) {
			return errGroupCycle
		}
		set := visibleSet(append(groupFields(u), namedValue{"parent_id", parentID}), at, by)
		set = append(set, bson.E{Key: "ancestors", Value: literal(ancestors)})
		if err := s.updateLiveGroup(ctx, tenantID, id, set); err != nil {
			return err
		}
		return s.rebaseSubtree(ctx, tenantID, id, ancestors)
	})
}

func (s *Store) rebaseSubtree(ctx context.Context, tenantID, id string, ancestors []string) error {
	below := bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$indexOfArray", Value: bson.A{"$ancestors", literal(id)}}}, 1}}}
	tail := bson.D{{Key: "$slice", Value: bson.A{"$ancestors", below, bson.D{{Key: "$size", Value: "$ancestors"}}}}}
	head := literal(append(slices.Clone(ancestors), id))
	set := bson.D{{Key: "ancestors", Value: bson.D{{Key: "$concatArrays", Value: bson.A{head, tail}}}}}
	_, err := s.db.Collection(groupsCollection).UpdateMany(ctx,
		bson.D{{Key: "tenant_id", Value: tenantID}, {Key: "ancestors", Value: id}},
		mongo.Pipeline{{{Key: "$set", Value: set}}})
	return err
}

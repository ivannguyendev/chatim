package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var directoryFields = []string{"tenant_id", "id", "display_name", "avatar_url", "state", "ver"}

type directoryDoc struct {
	TenantID    string      `bson:"tenant_id"`
	ID          string      `bson:"id"`
	DisplayName string      `bson:"display_name"`
	AvatarURL   string      `bson:"avatar_url"`
	State       store.State `bson:"state"`
	Ver         int64       `bson:"ver"`
}

func (d directoryDoc) entry() store.DirectoryEntry {
	return store.DirectoryEntry{
		TenantID: d.TenantID, ID: d.ID, DisplayName: d.DisplayName, AvatarURL: d.AvatarURL, State: d.State, Ver: d.Ver,
	}
}

func (s *Store) LoadPage(ctx context.Context, after store.UserKey, limit int) ([]store.DirectoryEntry, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be positive", apperr.ErrInvalidArgument)
	}
	filter := bson.D{}
	if after != (store.UserKey{}) {
		filter = bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: tenantKey(after.TenantID, after.ID)}}}}
	}
	projection := bson.D{{Key: "_id", Value: 0}}
	for _, f := range directoryFields {
		projection = append(projection, bson.E{Key: f, Value: 1})
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(limit)).SetProjection(projection)
	var docs []directoryDoc
	if err := s.findAll(ctx, usersCollection, filter, &docs, opts); err != nil {
		return nil, err
	}
	out := make([]store.DirectoryEntry, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.entry())
	}
	return out, nil
}

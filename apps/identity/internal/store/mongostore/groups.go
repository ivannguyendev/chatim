package mongostore

import (
	"context"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type groupDoc struct {
	TenantID    string           `bson:"tenant_id"`
	ID          string           `bson:"id"`
	Name        string           `bson:"name"`
	ParentID    string           `bson:"parent_id"`
	Ancestors   []string         `bson:"ancestors"`
	Mentionable bool             `bson:"mentionable"`
	State       store.GroupState `bson:"state"`
	Ver         int64            `bson:"ver"`
	MembersVer  int64            `bson:"members_ver"`
	CreatedAt   time.Time        `bson:"created_at"`
	UpdatedAt   time.Time        `bson:"updated_at"`
	UpdatedBy   string           `bson:"updated_by"`
}

type namedValue struct {
	name  string
	value any
}

func (s *Store) CreateGroup(ctx context.Context, g store.Group) error {
	return s.runOnce(ctx, func(ctx context.Context) error {
		if err := s.bumpGroupTree(ctx, g.TenantID); err != nil {
			return err
		}
		ancestors, err := s.pathUnder(ctx, g.TenantID, g.ParentID)
		if err != nil {
			return err
		}
		_, err = s.db.Collection(groupsCollection).InsertOne(ctx, bson.D{
			{Key: "_id", Value: tenantKey(g.TenantID, g.ID)},
			{Key: "tenant_id", Value: g.TenantID},
			{Key: "id", Value: g.ID},
			{Key: "name", Value: g.Name},
			{Key: "parent_id", Value: g.ParentID},
			{Key: "ancestors", Value: ancestors},
			{Key: "mentionable", Value: g.Mentionable},
			{Key: "state", Value: g.State},
			{Key: "ver", Value: int64(1)},
			{Key: "members_ver", Value: int64(0)},
			{Key: "created_at", Value: g.CreatedAt},
			{Key: "updated_at", Value: g.UpdatedAt},
			{Key: "updated_by", Value: g.UpdatedBy},
		})
		return err
	})
}

func (s *Store) Group(ctx context.Context, tenantID, id string) (store.Group, error) {
	var doc groupDoc
	err := s.db.Collection(groupsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: tenantKey(tenantID, id)}}).Decode(&doc)
	if err != nil {
		return store.Group{}, s.mapErr(err)
	}
	return store.Group{
		TenantID: doc.TenantID, ID: doc.ID, Name: doc.Name, ParentID: doc.ParentID, Mentionable: doc.Mentionable,
		State: doc.State, CreatedAt: doc.CreatedAt, UpdatedAt: doc.UpdatedAt, UpdatedBy: doc.UpdatedBy,
	}, nil
}

func (s *Store) bumpGroupTree(ctx context.Context, tenantID string) error {
	res, err := s.db.Collection(tenantsCollection).UpdateOne(ctx, bson.D{{Key: "_id", Value: tenantID}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "group_tree_ver", Value: int64(1)}}}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func (s *Store) liveGroup(ctx context.Context, tenantID, id string) (groupDoc, error) {
	var doc groupDoc
	err := s.db.Collection(groupsCollection).FindOne(ctx, liveGroupFilter(tenantID, id)).Decode(&doc)
	return doc, err
}

func (s *Store) pathUnder(ctx context.Context, tenantID, parentID string) ([]string, error) {
	if parentID == "" {
		return []string{}, nil
	}
	parent, err := s.liveGroup(ctx, tenantID, parentID)
	if err != nil {
		return nil, err
	}
	return append(slices.Clone(parent.Ancestors), parentID), nil
}

func liveGroupFilter(tenantID, id string) bson.D {
	return bson.D{{Key: "_id", Value: tenantKey(tenantID, id)}, {Key: "state", Value: store.GroupLive}}
}

func groupFields(u store.GroupUpdate) []namedValue {
	var fields []namedValue
	if u.Name != nil {
		fields = append(fields, namedValue{"name", *u.Name})
	}
	if u.Mentionable != nil {
		fields = append(fields, namedValue{"mentionable", *u.Mentionable})
	}
	return fields
}

func visibleSet(fields []namedValue, at time.Time, by string) bson.D {
	var values bson.D
	changes := bson.A{}
	for _, f := range fields {
		values = append(values, bson.E{Key: f.name, Value: literal(f.value)})
		changes = append(changes, differs(f.name, f.value))
	}
	return versionedSet(bson.D{{Key: "$or", Value: append(changes, false)}}, values, at, by)
}

func (s *Store) updateLiveGroup(ctx context.Context, tenantID, id string, set bson.D) error {
	res, err := s.db.Collection(groupsCollection).UpdateOne(ctx, liveGroupFilter(tenantID, id), mongo.Pipeline{{{Key: "$set", Value: set}}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

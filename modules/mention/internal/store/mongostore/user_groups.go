package mongostore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/mongobound"
)

var _ store.UserGroups = (*UserGroups)(nil)

type UserGroups struct {
	coll *mongo.Collection
}

func NewUserGroups(db *mongo.Database) *UserGroups {
	return &UserGroups{coll: db.Collection(userGroupsCollection)}
}

type userGroupsDoc struct {
	ID     string   `bson:"_id"`
	User   string   `bson:"user_id"`
	Groups []string `bson:"groups"`
	Ver    int64    `bson:"ver"`
}

func (s *UserGroups) Set(ctx context.Context, items []domain.UserGroups) ([]string, error) {
	if err := store.ValidateUserGroups(items); err != nil {
		return nil, err
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = tenantID(it.Tenant, it.User)
	}
	docs, err := findAll[userGroupsDoc](ctx, s.coll, mongobound.StringIDs(ids))
	if err != nil {
		return nil, fmt.Errorf("read user groups: %w", err)
	}
	stored := make(map[string]userGroupsDoc, len(docs))
	for _, d := range docs {
		stored[d.ID] = d
	}
	now := time.Now().UTC()
	changed, models := []string{}, make([]mongo.WriteModel, 0, len(items))
	for i, it := range items {
		ver, err := toInt64("user groups ver", it.Ver)
		if err != nil {
			return nil, err
		}
		cur, ok := stored[ids[i]]
		if ok && cur.Ver >= ver {
			continue
		}
		groups := domain.NormalizeGroups(it.Groups)
		models = append(models, newerUpsert(ids[i], ver, bson.D{
			{Key: "tenant", Value: it.Tenant}, {Key: "user_id", Value: it.User}, {Key: "groups", Value: groups},
			{Key: "updated_at", Value: now}, {Key: "updated_by", Value: it.UpdatedBy},
		}))
		if !ok || !slices.Equal(cur.Groups, groups) {
			changed = append(changed, it.User)
		}
	}
	if err := writeAll(ctx, s.coll, models); err != nil {
		return nil, fmt.Errorf("set user groups: %w", err)
	}
	return changed, nil
}

func (s *UserGroups) Of(ctx context.Context, tenant string, users []string) (map[string][]string, error) {
	if err := store.ValidateUsers(tenant, users); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(users))
	if len(users) == 0 {
		return out, nil
	}
	docs, err := findAll[userGroupsDoc](ctx, s.coll, userGroupsFilter(tenant, users))
	if err != nil {
		return nil, fmt.Errorf("user groups of %d users: %w", len(users), err)
	}
	for _, d := range docs {
		out[d.User] = append([]string{}, d.Groups...)
	}
	return out, nil
}

func userGroupsFilter(tenant string, users []string) bson.D {
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = tenantID(tenant, u)
	}
	return mongobound.StringIDs(ids)
}

func (s *UserGroups) MembersOfGroup(ctx context.Context, tenant, group, after string, limit int) ([]string, error) {
	if err := store.ValidateGroupMembersQuery(tenant, group, after, limit); err != nil {
		return nil, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "user_id", Value: 1}}).SetLimit(int64(limit)).
		SetProjection(bson.D{{Key: "user_id", Value: 1}})
	docs, err := findAll[userGroupsDoc](ctx, s.coll, groupMembersFilter(tenant, group, after), opts)
	if err != nil {
		return nil, fmt.Errorf("members of group %s: %w", group, err)
	}
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.User
	}
	return out, nil
}

func groupMembersFilter(tenant, group, after string) bson.D {
	return bson.D{{Key: "tenant", Value: tenant}, {Key: "groups", Value: group}, {Key: "user_id", Value: bson.D{{Key: "$gt", Value: after}}}}
}

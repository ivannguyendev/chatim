package mongostore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/mongobound"
)

var _ store.Groups = (*Groups)(nil)

const (
	groupTaggable    int64 = 1
	groupNotTaggable int64 = 2
)

type Groups struct {
	coll *mongo.Collection
}

func NewGroups(db *mongo.Database) *Groups {
	return &Groups{coll: db.Collection(groupsCollection)}
}

type groupDoc struct {
	ID        string `bson:"_id"`
	Tenant    string `bson:"tenant"`
	Group     string `bson:"group_id"`
	Name      string `bson:"name"`
	State     int64  `bson:"state"`
	Ver       int64  `bson:"ver"`
	UpdatedBy string `bson:"updated_by"`
}

func (s *Groups) Set(ctx context.Context, items []domain.Group) ([]string, error) {
	if err := store.ValidateGroups(items); err != nil {
		return nil, err
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = tenantID(it.Tenant, it.ID)
	}
	docs, err := findAll[groupDoc](ctx, s.coll, mongobound.StringIDs(ids))
	if err != nil {
		return nil, fmt.Errorf("read groups: %w", err)
	}
	stored := make(map[string]groupDoc, len(docs))
	for _, d := range docs {
		stored[d.ID] = d
	}
	now := time.Now().UTC()
	changed, models := []string{}, make([]mongo.WriteModel, 0, len(items))
	for i, it := range items {
		ver, err := toInt64("group ver", it.Ver)
		if err != nil {
			return nil, err
		}
		cur, ok := stored[ids[i]]
		if ok && cur.Ver >= ver {
			continue
		}
		state := groupStateOf(it.Active)
		models = append(models, newerUpsert(ids[i], ver, bson.D{
			{Key: "tenant", Value: it.Tenant}, {Key: "group_id", Value: it.ID}, {Key: "name", Value: it.Name},
			{Key: "state", Value: state}, {Key: "updated_at", Value: now}, {Key: "updated_by", Value: it.UpdatedBy},
		}))
		if !ok || cur.Name != it.Name || cur.State != state {
			changed = append(changed, it.ID)
		}
	}
	if err := writeAll(ctx, s.coll, models); err != nil {
		return nil, fmt.Errorf("set groups: %w", err)
	}
	return changed, nil
}

func (s *Groups) All(ctx context.Context, tenant string) ([]domain.Group, error) {
	if err := store.ValidateTenant(tenant); err != nil {
		return nil, err
	}
	docs, err := findAll[groupDoc](ctx, s.coll, tenantRange(tenant))
	if err != nil {
		return nil, fmt.Errorf("groups of %s: %w", tenant, err)
	}
	out := make([]domain.Group, 0, len(docs))
	for _, d := range docs {
		g, err := decodeGroup(d)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b domain.Group) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func groupStateOf(active bool) int64 {
	if active {
		return groupTaggable
	}
	return groupNotTaggable
}

func decodeGroup(d groupDoc) (domain.Group, error) {
	if d.State != groupTaggable && d.State != groupNotTaggable {
		return domain.Group{}, fmt.Errorf("%w: group state %d", errCorrupt, d.State)
	}
	ver, err := toUint64("group ver", d.Ver)
	if err != nil {
		return domain.Group{}, err
	}
	return domain.Group{Tenant: d.Tenant, ID: d.Group, Name: d.Name, Active: d.State == groupTaggable, Ver: ver, UpdatedBy: d.UpdatedBy}, nil
}

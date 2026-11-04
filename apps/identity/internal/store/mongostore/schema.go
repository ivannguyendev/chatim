package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

const (
	tenantsCollection          = "tenants"
	usersCollection            = "users"
	groupsCollection           = "groups"
	trustedIssuersCollection   = "trusted_issuers"
	trustedKeysCollection      = "trusted_keys"
	signingKeysCollection      = "signing_keys"
	signingKeyClaimsCollection = "signing_key_claims"
	revokedSessionsCollection  = "revoked_sessions"
	slotsCollection            = "slots"
	namespaceExistsCode        = 48
)

type collectionShape int8

const (
	shapeAbsent collectionShape = iota
	shapeClustered
	shapePlain
)

type schemaSurvey struct {
	catalog mongoindex.Catalog
	shapes  map[string]collectionShape
	reports []mongoindex.Report
}

func Indexes() mongoindex.Catalog {
	return mongoindex.Catalog{Collections: []mongoindex.Collection{
		{Name: tenantsCollection},
		{Name: usersCollection, Indexes: []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "groups", Value: 1}}},
		}},
		{Name: groupsCollection, Indexes: []mongo.IndexModel{
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "ancestors", Value: 1}}},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "parent_id", Value: 1}, {Key: "state", Value: 1}}},
		}},
		{Name: trustedIssuersCollection},
		{Name: trustedKeysCollection},
		{Name: signingKeysCollection},
		{Name: signingKeyClaimsCollection},
		{Name: revokedSessionsCollection, Indexes: []mongo.IndexModel{
			{Keys: bson.D{{Key: "expires_at", Value: 1}}},
		}},
		{Name: slotsCollection},
	}}
}

func (s *Store) Schema(ctx context.Context) (store.SchemaState, error) {
	sv, err := s.survey(ctx)
	if err != nil {
		return store.SchemaState{}, err
	}
	var state store.SchemaState
	for _, r := range sv.reports {
		switch sv.shapes[r.Collection] {
		case shapeAbsent:
			state.Missing = append(state.Missing, "collection "+r.Collection+" absent")
			continue
		case shapePlain:
			state.Missing = append(state.Missing, "collection "+r.Collection+" not clustered on _id")
		case shapeClustered:
		}
		for _, name := range r.MissingNames() {
			state.Missing = append(state.Missing, "index "+r.Collection+" "+name+" missing")
		}
		if len(r.Outdated) > 0 || len(r.Conflicts) > 0 {
			state.Outdated = append(state.Outdated, r.Collection)
		}
	}
	return state, nil
}

func (s *Store) Migrate(ctx context.Context, apply bool) ([]string, error) {
	sv, err := s.survey(ctx)
	if err != nil {
		return nil, err
	}
	lines := sv.lines()
	if !apply {
		return lines, nil
	}
	if err := sv.refusal(); err != nil {
		return lines, err
	}
	for _, name := range sv.catalog.Names() {
		if sv.shapes[name] != shapeAbsent {
			continue
		}
		if err := s.createClustered(ctx, name); err != nil {
			return lines, err
		}
	}
	if _, err := mongoindex.Apply(ctx, s.db, sv.catalog); err != nil {
		if errors.Is(err, mongoindex.ErrIndexConflict) {
			return lines, fmt.Errorf("%w: %w", apperr.ErrFailedPrecondition, err)
		}
		return lines, s.mapErr(err)
	}
	return lines, nil
}

func (s *Store) survey(ctx context.Context) (schemaSurvey, error) {
	cat := Indexes()
	specs, err := s.db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: bson.D{{Key: "$in", Value: cat.Names()}}}})
	if err != nil {
		return schemaSurvey{}, s.mapErr(fmt.Errorf("list collections: %w", err))
	}
	shapes := make(map[string]collectionShape, len(cat.Collections))
	for _, name := range cat.Names() {
		shapes[name] = shapeAbsent
	}
	for _, spec := range specs {
		shapes[spec.Name] = shapePlain
		if clusteredOnID(spec.Options) {
			shapes[spec.Name] = shapeClustered
		}
	}
	reports, err := mongoindex.Inspect(ctx, s.db, cat)
	if err != nil {
		return schemaSurvey{}, s.mapErr(err)
	}
	return schemaSurvey{catalog: cat, shapes: shapes, reports: reports}, nil
}

func (sv schemaSurvey) lines() []string {
	var out []string
	for i, r := range sv.reports {
		switch sv.shapes[r.Collection] {
		case shapeAbsent:
			out = append(out, "collection "+r.Collection+" absent")
			r = mongoindex.Report{Collection: r.Collection, Missing: sv.catalog.Collections[i].Indexes}
		case shapePlain:
			out = append(out, "collection "+r.Collection+" not clustered on _id, recreate it")
		case shapeClustered:
		}
		if r.Absent {
			continue
		}
		out = append(out, r.Lines()...)
	}
	return out
}

func (sv schemaSurvey) refusal() error {
	var found []string
	for _, r := range sv.reports {
		if sv.shapes[r.Collection] == shapePlain {
			found = append(found, "collection "+r.Collection+" not clustered on _id")
		}
		for _, c := range r.Conflicts {
			found = append(found, r.Collection+" conflict "+c.Name+": "+c.Detail)
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("%w: %s", apperr.ErrFailedPrecondition, strings.Join(found, "; "))
	}
	return nil
}

func (s *Store) createClustered(ctx context.Context, name string) error {
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	if err := s.db.CreateCollection(ctx, name, opts); err != nil && !hasCode(err, namespaceExistsCode) {
		return s.mapErr(fmt.Errorf("create collection %s: %w", name, err))
	}
	specs, err := s.db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil {
		return s.mapErr(fmt.Errorf("list collection %s: %w", name, err))
	}
	if len(specs) != 1 || !clusteredOnID(specs[0].Options) {
		return fmt.Errorf("%w: collection %s not clustered on _id", apperr.ErrFailedPrecondition, name)
	}
	return nil
}

func clusteredOnID(opts bson.Raw) bool {
	dir, ok := opts.Lookup("clusteredIndex", "key", "_id").AsInt64OK()
	return ok && dir == 1
}

func hasCode(err error, code int) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && se.HasErrorCode(code)
}

package mongoindex

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	namespaceNotFoundCode = 26
	indexNotFoundCode     = 27
)

type Reporter interface {
	Report(ctx context.Context, r Report)
}

func List(ctx context.Context, coll *mongo.Collection) ([]Existing, bool, error) {
	cmd := bson.D{{Key: "listIndexes", Value: coll.Name()}, {Key: "includeIndexBuildInfo", Value: true}}
	cur, err := coll.Database().RunCommandCursor(ctx, cmd)
	if hasCode(err, namespaceNotFoundCode) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("list indexes of %s: %w", coll.Name(), err)
	}
	var docs []bson.Raw
	if err := cur.All(ctx, &docs); err != nil {
		return nil, false, fmt.Errorf("list indexes of %s: %w", coll.Name(), err)
	}
	out := make([]Existing, 0, len(docs))
	for _, d := range docs {
		e, err := existingOf(d)
		if err != nil {
			return nil, false, fmt.Errorf("list indexes of %s: %w", coll.Name(), err)
		}
		out = append(out, e)
	}
	return out, true, nil
}

func existingOf(doc bson.Raw) (Existing, error) {
	spec := doc
	if s, ok := doc.Lookup("spec").DocumentOK(); ok {
		spec = s
	}
	_, building := doc.Lookup("indexBuildInfo").DocumentOK()
	name, _ := spec.Lookup("name").StringValueOK()
	keys, ok := spec.Lookup("key").DocumentOK()
	if !ok || name == "" {
		return Existing{}, fmt.Errorf("index spec without name or key: %s", spec)
	}
	p, err := Pattern(keys)
	if err != nil {
		return Existing{}, err
	}
	return Existing{Name: name, Keys: p, Options: existingOptions(spec), Building: building}, nil
}

func Inspect(ctx context.Context, db *mongo.Database, cat Catalog) ([]Report, error) {
	out := make([]Report, 0, len(cat.Collections))
	for _, c := range cat.Collections {
		r, err := inspect(ctx, db, cat, c)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func inspect(ctx context.Context, db *mongo.Database, cat Catalog, c Collection) (Report, error) {
	existing, found, err := List(ctx, db.Collection(c.Name))
	if err != nil {
		return Report{}, err
	}
	if !found {
		return Report{Collection: c.Name, Absent: true}, nil
	}
	return Compare(c, cat.replacementsOf(c.Name), existing)
}

func Apply(ctx context.Context, db *mongo.Database, cat Catalog) ([]string, error) {
	reports, err := Inspect(ctx, db, cat)
	if err != nil {
		return nil, err
	}
	if err := conflictsOf(reports); err != nil {
		return nil, err
	}
	var created []string
	for _, r := range reports {
		if r.Absent || len(r.Missing) == 0 {
			continue
		}
		if _, err := db.Collection(r.Collection).Indexes().CreateMany(ctx, r.Missing); err != nil {
			return created, fmt.Errorf("create indexes on %s: %w", r.Collection, err)
		}
		for _, name := range r.MissingNames() {
			created = append(created, r.Collection+" "+name)
		}
	}
	return created, nil
}

func DropOld(ctx context.Context, db *mongo.Database, cat Catalog) ([]string, error) {
	reports, err := Inspect(ctx, db, cat)
	if err != nil {
		return nil, err
	}
	plan, err := dropPlan(reports)
	if err != nil {
		return nil, err
	}
	var dropped []string
	for _, rep := range plan {
		if err := db.Collection(rep.Collection).Indexes().DropOne(ctx, rep.Old); err != nil && !hasCode(err, indexNotFoundCode) {
			return dropped, fmt.Errorf("drop index %s on %s: %w", rep.Old, rep.Collection, err)
		}
		dropped = append(dropped, rep.Collection+" "+rep.Old)
	}
	return dropped, nil
}

func Ensure(ctx context.Context, db *mongo.Database, cat Catalog, reporters ...Reporter) error {
	for _, c := range cat.Collections {
		r, err := inspect(ctx, db, cat, c)
		if err != nil {
			return err
		}
		if r, err = createOnEmpty(ctx, db, r); err != nil {
			return err
		}
		for _, rep := range reporters {
			rep.Report(ctx, r)
		}
	}
	return nil
}

func createOnEmpty(ctx context.Context, db *mongo.Database, r Report) (Report, error) {
	if r.Absent || len(r.Missing) == 0 {
		return r, nil
	}
	coll := db.Collection(r.Collection)
	empty, err := isEmpty(ctx, coll)
	if err != nil || !empty {
		return r, err
	}
	if _, err := coll.Indexes().CreateMany(ctx, r.Missing); err != nil {
		return r, fmt.Errorf("create indexes on %s: %w", r.Collection, err)
	}
	r.Missing = nil
	return r, nil
}

func isEmpty(ctx context.Context, coll *mongo.Collection) (bool, error) {
	err := coll.FindOne(ctx, bson.D{}, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}})).Err()
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("probe documents of %s: %w", coll.Name(), err)
	default:
		return false, nil
	}
}

func hasCode(err error, code int) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && se.HasErrorCode(code)
}

package mongostore

import (
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type statementCost struct {
	lookupCost
	filter bson.Raw
	keys   int
}

func explainEachUpdate(t *testing.T, db *mongo.Database, capture *updateCapture, coll string) []statementCost {
	t.Helper()
	sent := capture.take(coll)
	if len(sent) == 0 {
		t.Fatalf("no update on %s was sent", coll)
	}
	var out []statementCost
	for _, raw := range sent {
		base, stmts := splitUpdate(t, raw)
		for _, stmt := range stmts {
			out = append(out, explainStatement(t, db, coll, append(slices.Clone(base), bson.E{Key: "updates", Value: bson.A{stmt}}), stmt))
		}
	}
	return out
}

func splitUpdate(t *testing.T, raw bson.Raw) (bson.D, []bson.RawValue) {
	t.Helper()
	elems, err := raw.Elements()
	if err != nil {
		t.Fatalf("captured update: %v", err)
	}
	var base bson.D
	var stmts []bson.RawValue
	for _, e := range elems {
		switch {
		case e.Key() == "updates":
			if stmts, err = e.Value().Array().Values(); err != nil {
				t.Fatalf("captured updates: %v", err)
			}
		case !slices.Contains(sessionFields, e.Key()) && e.Key() != "writeConcern":
			base = append(base, bson.E{Key: e.Key(), Value: e.Value()})
		}
	}
	return base, stmts
}

func explainStatement(t *testing.T, db *mongo.Database, coll string, cmd bson.D, stmt bson.RawValue) statementCost {
	t.Helper()
	res, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: cmd}, {Key: "verbosity", Value: "executionStats"}}).Raw()
	if err != nil {
		t.Fatalf("explain %v: %v", cmd, err)
	}
	filter := stmt.Document().Lookup("q").Document()
	return statementCost{
		lookupCost: lookupCost{
			examined: int(res.Lookup("executionStats", "totalDocsExamined").AsInt64()),
			returned: int(res.Lookup("executionStats", "nReturned").AsInt64()),
			plan:     res.Lookup("queryPlanner", "winningPlan").String(),
		},
		filter: filter,
		keys:   keysInFilter(t, db, coll, filter),
	}
}

func keysInFilter(t *testing.T, db *mongo.Database, coll string, filter bson.Raw) int {
	t.Helper()
	id := filter.Lookup("_id")
	if id.Type != bson.TypeEmbeddedDocument {
		return 1
	}
	n, err := db.Collection(coll).CountDocuments(t.Context(), bson.D{{Key: "_id", Value: id}})
	if err != nil {
		t.Fatalf("count keys of %s: %v", id, err)
	}
	return max(int(n), 1)
}

func expectPointWrites(t *testing.T, name string, costs []statementCost, statements, targets int, plans ...string) {
	t.Helper()
	plans = append(plans, "CLUSTERED_IXSCAN", "IDHACK", "EXPRESS_UPDATE")
	if len(costs) != statements {
		t.Fatalf("%s: %d write statements, want %d", name, len(costs), statements)
	}
	keys := 0
	for i, c := range costs {
		keys += c.keys
		if strings.Contains(c.filter.String(), `"$or"`) {
			t.Errorf("%s statement %d: filter %s holds an $or", name, i, c.filter)
		}
		if strings.Contains(c.plan, "COLLSCAN") || strings.Contains(c.plan, `"OR"`) || !containsAny(c.plan, plans) {
			t.Errorf("%s statement %d: plan %s; want one bounded %v and no COLLSCAN or OR", name, i, c.plan, plans)
		}
		if c.examined > 2*c.keys {
			t.Errorf("%s statement %d: examined %d docs for %d keys, want at most %d", name, i, c.examined, c.keys, 2*c.keys)
		}
	}
	if keys != targets {
		t.Errorf("%s: statements cover %d keys, want %d", name, keys, targets)
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

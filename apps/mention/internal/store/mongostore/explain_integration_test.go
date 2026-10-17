package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
)

type plan struct {
	stages   []string
	indexes  []string
	examined int64
	returned int64
}

func explain(t *testing.T, db *mongo.Database, cmd bson.D) plan {
	t.Helper()
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: cmd}, {Key: "verbosity", Value: "executionStats"}}).Raw()
	if err != nil {
		t.Fatalf("explain %v: %v", cmd, err)
	}
	var p plan
	walkWinning(raw, func(d bson.Raw) {
		p.stages = append(p.stages, d.Lookup("stage").StringValue())
		if name, ok := d.Lookup("indexName").StringValueOK(); ok {
			p.indexes = append(p.indexes, name)
		}
	})
	if stats, ok := raw.Lookup("executionStats").DocumentOK(); ok {
		p.examined = stats.Lookup("totalDocsExamined").AsInt64()
		p.returned = stats.Lookup("nReturned").AsInt64()
	}
	return p
}

func walkWinning(doc bson.Raw, visit func(bson.Raw)) {
	elems, _ := doc.Elements()
	for _, e := range elems {
		if e.Key() == "winningPlan" {
			walkStages(e.Value(), visit)
			continue
		}
		if d, ok := e.Value().DocumentOK(); ok {
			walkWinning(d, visit)
		}
	}
}

func walkStages(v bson.RawValue, visit func(bson.Raw)) {
	if a, ok := v.ArrayOK(); ok {
		vals, _ := a.Values()
		for _, x := range vals {
			walkStages(x, visit)
		}
		return
	}
	d, ok := v.DocumentOK()
	if !ok {
		return
	}
	if _, isStage := d.Lookup("stage").StringValueOK(); isStage {
		visit(d)
	}
	elems, _ := d.Elements()
	for _, e := range elems {
		walkStages(e.Value(), visit)
	}
}

func findCmd(coll string, filter, sort bson.D, limit int) bson.D {
	cmd := bson.D{{Key: "find", Value: coll}, {Key: "filter", Value: filter}}
	if sort != nil {
		cmd = append(cmd, bson.E{Key: "sort", Value: sort})
	}
	if limit > 0 {
		cmd = append(cmd, bson.E{Key: "limit", Value: limit})
	}
	return cmd
}

func assertOnlyIndex(t *testing.T, name string, p plan, index string) {
	t.Helper()
	t.Logf("%s: stages %v (%d), examined %d, returned %d", name, p.stages[:min(len(p.stages), 5)], len(p.stages), p.examined, p.returned)
	if slices.Contains(p.stages, "SORT") || slices.Contains(p.stages, "COLLSCAN") || !slices.Contains(p.indexes, index) {
		t.Fatalf("%s: stages %v on %v, want %s scans without a blocking SORT", name, p.stages, p.indexes, index)
	}
	for _, ix := range p.indexes {
		if ix != index {
			t.Fatalf("%s: plan uses %s, want only %s", name, ix, index)
		}
	}
}

func assertClusteredBounded(t *testing.T, name string, p plan, keys int) {
	t.Helper()
	t.Logf("%s: stages %v, examined %d, returned %d", name, p.stages, p.examined, p.returned)
	if slices.Contains(p.stages, "COLLSCAN") || len(p.indexes) > 0 || p.examined > int64(2*keys+1) {
		t.Fatalf("%s: stages %v on %v examined %d for %d keys, want bounded clustered scans", name, p.stages, p.indexes, p.examined, keys)
	}
	if !slices.ContainsFunc(p.stages, func(s string) bool { return s == "CLUSTERED_IXSCAN" || s == "EXPRESS_CLUSTERED_IXSCAN" }) {
		t.Fatalf("%s: stages %v, want a clustered _id scan", name, p.stages)
	}
}

func TestMentionsOfScansTheMessageKeyIndex(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	m := NewMentions(db)
	for i := range 6 {
		mustApplyAll(t, m, itPlace(itRoom, uint64(i+1), itTime, itMinh, itOps, itAll))
	}
	key := domain.MessageKey{Room: itRoom, Seq: 3}
	p := explain(t, db, findCmd(mentionsCollection, mentionsOfFilter("acme", key), nil, 0))
	assertOnlyIndex(t, "Of", p, "message_key_1")
	if p.returned != 3 || p.examined != 3 {
		t.Fatalf("Of examined %d and returned %d docs, want 3 and 3", p.examined, p.returned)
	}
}

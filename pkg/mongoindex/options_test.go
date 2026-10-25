package mongoindex

import (
	"errors"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	partialFilter  = bson.D{{Key: "_state", Value: bson.D{{Key: "$exists", Value: true}}}}
	statePending   = mongo.IndexModel{Keys: bson.D{{Key: "_state_at", Value: 1}, {Key: "_id", Value: 1}}, Options: options.Index().SetName("state_pending").SetPartialFilterExpression(partialFilter)}
	uniqueHidden   = mongo.IndexModel{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "seq", Value: 1}}, Options: options.Index().SetUnique(true)}
	optionsCatalog = Collection{Name: "hidden", Indexes: []mongo.IndexModel{statePending, uniqueHidden}}
)

func existingSpec(t *testing.T, spec bson.D) Existing {
	t.Helper()
	raw, err := bson.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	e, err := existingOf(raw)
	if err != nil {
		t.Fatalf("existingOf: %v", err)
	}
	return e
}

func TestDeclaredAndListedOptionsCanonicalizeAlike(t *testing.T) {
	listed := []Existing{
		existingSpec(t, bson.D{{Key: "key", Value: bson.D{{Key: "_state_at", Value: 1}, {Key: "_id", Value: 1}}}, {Key: "name", Value: "state_pending"}, {Key: "partialFilterExpression", Value: partialFilter}}),
		existingSpec(t, bson.D{{Key: "key", Value: bson.D{{Key: "user_id", Value: 1}, {Key: "seq", Value: 1}}}, {Key: "name", Value: "user_id_1_seq_1"}, {Key: "unique", Value: true}}),
	}
	r, err := Compare(optionsCatalog, nil, listed)
	if err != nil || r.Stale() || len(r.Unknown) != 0 {
		t.Fatalf("Compare = %+v, %v; want matching options", r, err)
	}
	ttl := mongo.IndexModel{Keys: bson.D{{Key: "at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(60).SetSparse(true).SetHidden(true).SetCollation(&options.Collation{Locale: "en"})}
	declared, err := declaredOptions(ttl)
	listedTTL := existingSpec(t, bson.D{{Key: "key", Value: bson.D{{Key: "at", Value: 1}}}, {Key: "name", Value: "at_1"}, {Key: "expireAfterSeconds", Value: int64(60)}, {Key: "sparse", Value: true}, {Key: "hidden", Value: true}, {Key: "collation", Value: bson.D{{Key: "locale", Value: "en"}, {Key: "strength", Value: 3}}}})
	if err != nil || declared != listedTTL.Options || declared != "sparse,hidden,expireAfterSeconds=60,collation=en" {
		t.Fatalf("declared %q (%v), listed %q; want the same canonical options", declared, err, listedTTL.Options)
	}
}

func TestCompareReportsOptionConflictsAndSameNameOtherKeys(t *testing.T) {
	listed := []Existing{
		existingSpec(t, bson.D{{Key: "key", Value: bson.D{{Key: "_state_at", Value: 1}, {Key: "_id", Value: 1}}}, {Key: "name", Value: "_state_at_1__id_1"}}),
		existingSpec(t, bson.D{{Key: "key", Value: bson.D{{Key: "user_id", Value: 1}, {Key: "seq", Value: 1}}}, {Key: "name", Value: "user_id_1_seq_1"}}),
	}
	r, err := Compare(optionsCatalog, nil, listed)
	if err != nil || len(r.Conflicts) != 2 || len(r.Missing) != 0 || len(r.Unknown) != 0 || !r.Stale() {
		t.Fatalf("Compare = %+v, %v; want two option conflicts and nothing missing", r, err)
	}
	lines := strings.Join(r.Lines(), "\n")
	for _, want := range []string{"hidden conflict _state_at_1__id_1: declared state_pending with options [partialFilterExpression=", "hidden conflict user_id_1_seq_1: declared user_id_1_seq_1 with options [unique], found []"} {
		if !strings.Contains(lines, want) {
			t.Errorf("lines %q lack %q", lines, want)
		}
	}
	sameName := []Existing{{Name: "state_pending", Keys: "x:1"}}
	r, err = Compare(Collection{Name: "rooms", Indexes: []mongo.IndexModel{statePending}}, nil, sameName)
	if err != nil || len(r.Missing) != 0 || len(r.Conflicts) != 1 || !strings.Contains(r.Conflicts[0].Detail, "same name, declared keys _state_at:1,_id:1, found x:1") {
		t.Fatalf("Compare(same name) = %+v, %v; want a same-name conflict instead of missing", r, err)
	}
	if err := conflictsOf([]Report{r}); !errors.Is(err, ErrIndexConflict) || !strings.Contains(err.Error(), "rooms state_pending") {
		t.Fatalf("conflictsOf = %v, want ErrIndexConflict naming rooms state_pending", err)
	}
}

package mongostore

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var sessionFields = []string{"lsid", "$clusterTime", "$db", "readConcern", "$readPreference", "txnNumber", "autocommit", "startTransaction"}

type findCapture struct {
	mu   sync.Mutex
	last map[string]bson.Raw
}

type lookupCost struct {
	examined, returned int
	plan               string
}

func (c *findCapture) started(_ context.Context, e *event.CommandStartedEvent) {
	if e.CommandName != "find" {
		return
	}
	coll, _ := e.Command.Lookup("find").StringValueOK()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last[coll] = slices.Clone(e.Command)
}

func (c *findCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.last)
}

func (c *findCapture) lastFind(t *testing.T, coll string) bson.D {
	t.Helper()
	c.mu.Lock()
	raw, ok := c.last[coll]
	c.mu.Unlock()
	if !ok {
		t.Fatalf("no find on %s was sent", coll)
	}
	elems, err := raw.Elements()
	if err != nil {
		t.Fatalf("captured find: %v", err)
	}
	var out bson.D
	for _, e := range elems {
		if !slices.Contains(sessionFields, e.Key()) {
			out = append(out, bson.E{Key: e.Key(), Value: e.Value()})
		}
	}
	return out
}

func monitoredStore(t *testing.T) (*Store, *mongo.Database, *findCapture) {
	t.Helper()
	itClient(t)
	capture := &findCapture{last: map[string]bson.Raw{}}
	opts := options.Client().ApplyURI(os.Getenv(itMongoURIEnv)).SetMonitor(&event.CommandMonitor{Started: capture.started})
	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	s, db := itStore(t, client)
	return s, db, capture
}

func explainLast(t *testing.T, db *mongo.Database, capture *findCapture, coll string) lookupCost {
	t.Helper()
	cmd := capture.lastFind(t, coll)
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: cmd}, {Key: "verbosity", Value: "executionStats"}}).Raw()
	if err != nil {
		t.Fatalf("explain %v: %v", cmd, err)
	}
	stats := raw.Lookup("executionStats")
	return lookupCost{
		examined: int(stats.Document().Lookup("totalDocsExamined").AsInt64()),
		returned: int(stats.Document().Lookup("nReturned").AsInt64()),
		plan:     raw.Lookup("queryPlanner", "winningPlan").String(),
	}
}

func expectBounded(t *testing.T, name string, c lookupCost, keys, wantReturned int) {
	t.Helper()
	if c.returned != wantReturned {
		t.Errorf("%s: explain returned %d docs, want %d", name, c.returned, wantReturned)
	}
	if c.examined > 2*keys+1 || strings.Contains(c.plan, "COLLSCAN") {
		t.Errorf("%s: examined %d docs for %d keys (returned %d), plan %s; want at most %d and no COLLSCAN", name, c.examined, keys, c.returned, c.plan, 2*keys+1)
	}
}

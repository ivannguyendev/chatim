package mongostore

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type updateCapture struct {
	mu   sync.Mutex
	sent map[string][]bson.Raw
}

func (c *updateCapture) started(_ context.Context, e *event.CommandStartedEvent) {
	if e.CommandName != "update" {
		return
	}
	coll, _ := e.Command.Lookup("update").StringValueOK()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent[coll] = append(c.sent[coll], slices.Clone(e.Command))
}

func (c *updateCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.sent)
}

func (c *updateCapture) take(coll string) []bson.Raw {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.sent[coll]
	delete(c.sent, coll)
	return out
}

func monitoredStateStore(t *testing.T) (*Store, *mongo.Database, *findCapture, *updateCapture) {
	t.Helper()
	itClient(t)
	finds := &findCapture{last: map[string]bson.Raw{}}
	updates := &updateCapture{sent: map[string][]bson.Raw{}}
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		finds.started(ctx, e)
		updates.started(ctx, e)
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(os.Getenv(itMongoURIEnv)).SetMonitor(monitor))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	s, db := itStore(t, client)
	return s, db, finds, updates
}

func pendingIndexKeys(t *testing.T, db *mongo.Database, coll string) int {
	t.Helper()
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "validate", Value: coll}}).Raw()
	if err != nil {
		t.Fatalf("validate %s: %v", coll, err)
	}
	n, ok := raw.Lookup("keysPerIndex", statePendingIndex).AsInt64OK()
	if !ok {
		t.Fatalf("validate %s: no keys for %s in %s", coll, statePendingIndex, raw.Lookup("keysPerIndex"))
	}
	return int(n)
}

func expectPendingIndexKeys(t *testing.T, db *mongo.Database, want int, colls ...string) {
	t.Helper()
	for _, coll := range colls {
		if got := pendingIndexKeys(t, db, coll); got != want {
			t.Fatalf("%s %s keys = %d, want %d", coll, statePendingIndex, got, want)
		}
	}
}

func expectPendingScan(t *testing.T, name string, c lookupCost, limit int) {
	t.Helper()
	if !strings.Contains(c.plan, statePendingIndex) || strings.Contains(c.plan, "COLLSCAN") || strings.Contains(c.plan, `"SORT"`) {
		t.Errorf("%s: plan %s; want %s scans and no COLLSCAN or blocking SORT", name, c.plan, statePendingIndex)
	}
	if c.examined > limit+1 || c.returned != limit+1 {
		t.Errorf("%s: examined %d, returned %d; want %d of each", name, c.examined, c.returned, limit+1)
	}
}

func flaggedMessages() []store.StateTarget {
	var out []store.StateTarget
	for seq := uint64(10); seq <= 19; seq++ {
		if seq != 15 {
			out = append(out, store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: boundedRoomA, Seq: seq})
		}
	}
	return append(out,
		store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: boundedRoomB, Seq: boundedMain},
		store.StateTarget{Collection: store.StateMessages, Bit: store.BitMsgCreated, Room: boundedRoomC, Thread: 7, Seq: 3})
}

func flaggedVersioned() ([]store.StateTarget, []store.StateTarget) {
	member := func(room uint64, user string) store.StateTarget {
		return store.StateTarget{Collection: store.StateMembers, Bit: store.BitMember, Room: room, User: user, Ver: 3}
	}
	reaction := func(seq uint64, user string) store.StateTarget {
		return store.StateTarget{Collection: store.StateInteractions, Bit: store.BitInteraction, Room: boundedRoomA, Seq: seq, User: user, Kind: keys.ReactionKind, Ver: 2}
	}
	members := []store.StateTarget{member(boundedRoomA, "x"), member(boundedRoomA, boundedLong), member(boundedRoomA, "m05"), member(boundedRoomB, "m30")}
	return members, []store.StateTarget{reaction(3, "bob"), reaction(3, "carolyn"), reaction(30, "bob")}
}

func seedStates(t *testing.T, db *mongo.Database, targets []store.StateTarget, at time.Time) {
	t.Helper()
	for _, target := range targets {
		if err := (stateSeeder{db: db}).SeedState(t.Context(), target, at); err != nil {
			t.Fatalf("SeedState(%+v): %v", target, err)
		}
	}
}

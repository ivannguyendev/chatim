package mongostore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func foreignDatabase(t *testing.T, s *Store) *mongo.Database {
	t.Helper()
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	db := s.client.Database("core_it_"+hex.EncodeToString(suffix), options.Database().SetWriteConcern(writeconcern.Majority()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = db.Drop(ctx)
	})
	return db
}

func idleCursorCount(t *testing.T, s *Store, id int64) int {
	t.Helper()
	cur, err := s.client.Database("admin").Aggregate(t.Context(), mongo.Pipeline{
		{{Key: "$currentOp", Value: bson.D{{Key: "idleCursors", Value: true}, {Key: "allUsers", Value: true}}}},
		{{Key: "$match", Value: bson.D{{Key: "cursor.cursorId", Value: id}}}},
	})
	if err != nil {
		t.Fatalf("$currentOp: %v", err)
	}
	var ops []bson.M
	if err := cur.All(t.Context(), &ops); err != nil {
		t.Fatalf("read $currentOp: %v", err)
	}
	return len(ops)
}

func TestStreamIsLostWhenTheDatabaseIsDropped(t *testing.T) {
	s := testDatabase(t)
	cs := openStream(t, s, streamStart(t, s))
	if err := s.db.Drop(t.Context()); err != nil {
		t.Fatalf("drop database: %v", err)
	}
	if _, err := nextResult(t, cs); !errors.Is(err, store.ErrStreamLost) {
		t.Fatalf("Next after dropDatabase = %v; want ErrStreamLost", err)
	}
}

func TestIdleStreamTokenAdvancesPastWritesOfAnotherDatabase(t *testing.T) {
	s := testDatabase(t)
	cs := openStream(t, s, streamStart(t, s))
	first := idleNext(t, cs)
	if _, err := foreignDatabase(t, s).Collection("messages").InsertOne(t.Context(), bson.D{{Key: "text", Value: "hi"}}); err != nil {
		t.Fatalf("foreign write: %v", err)
	}
	second := idleNext(t, cs)
	if bytes.Equal(first, second) {
		t.Fatalf("idle token stayed %x after a foreign write; want it to advance", first)
	}
}

func TestStreamCloseKillsTheCursorEvenWithACancelledContext(t *testing.T) {
	s := testDatabase(t)
	cs, err := s.OpenStream(t.Context(), streamStart(t, s))
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	idleNext(t, cs)
	id := streamCursorID(cs)
	if id == 0 || idleCursorCount(t, s, id) != 1 {
		t.Fatalf("cursor %d not idle on the server before Close", id)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cs.Close(cancelled); err != nil {
		t.Fatalf("Close with a cancelled ctx: %v", err)
	}
	if n := idleCursorCount(t, s, id); n != 0 {
		t.Fatalf("cursor %d still on the server after Close (%d)", id, n)
	}
}

func TestStreamEventIsCaughtUpOnlyWhenItEmptiesTheBatch(t *testing.T) {
	s := testDatabase(t)
	from := streamStart(t, s)
	for _, id := range []string{"u1", "u2", "u3"} {
		mustCreateUser(t, s, "shop", id, "Name "+id, "")
	}
	cs := openStream(t, s, from)
	var caughtUp []bool
	for _, id := range []string{"u1", "u2", "u3"} {
		ev := nextEvent(t, cs)
		expectUser(t, ev, id, "Name "+id, 1)
		caughtUp = append(caughtUp, ev.CaughtUp)
	}
	if caughtUp[0] || caughtUp[1] || !caughtUp[2] {
		t.Fatalf("caught up per event = %v; want only the last event of the batch", caughtUp)
	}
	if token := idleNext(t, cs); len(token) == 0 {
		t.Fatal("idle Next gave no token")
	}
}

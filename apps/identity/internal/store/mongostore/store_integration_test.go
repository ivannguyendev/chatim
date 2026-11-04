package mongostore

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const conflictWait = 30 * time.Second

func TestOpenIsLazyAndPingFailsWithinTheDeadline(t *testing.T) {
	itURI(t)
	s, err := Open(testOptions("mongodb://no-such-host.invalid:27017", "identity_it_lazy"))
	if err != nil {
		t.Fatalf("Open with an unreachable host = %v; want nil", err)
	}
	t.Cleanup(s.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = s.Ping(ctx)
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Ping = %v; want unavailable", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Ping took %v; want it bounded by the ctx deadline", took)
	}
}

func TestPingReachesTheServer(t *testing.T) {
	s := testDatabase(t)
	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("Ping = %v; want nil", err)
	}
}

func TestRunOnceConflictReturnsUnavailableAndWritesOnce(t *testing.T) {
	s := testDatabase(t)
	probe := s.db.Collection("probe")
	insertDoc(t, s, "probe", bson.D{{Key: "_id", Value: "doc"}, {Key: "count", Value: 0}})
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	holdBeforeCommit(t, func() {
		arrived <- struct{}{}
		<-release
	})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			errs <- s.runOnce(context.Background(), func(ctx context.Context) error {
				_, err := probe.UpdateOne(ctx, bson.D{{Key: "_id", Value: "doc"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "count", Value: 1}}}})
				return err
			})
		}()
	}
	waitFor(t, arrived, "first transaction at the commit barrier")
	loser := waitFor(t, errs, "conflicting transaction")
	if !errors.Is(loser, apperr.ErrUnavailable) {
		t.Fatalf("conflicting runOnce = %v; want unavailable", loser)
	}
	close(release)
	if winner := waitFor(t, errs, "held transaction"); winner != nil {
		t.Fatalf("held runOnce = %v; want nil", winner)
	}
	var doc struct {
		Count int `bson:"count"`
	}
	if err := probe.FindOne(t.Context(), bson.D{{Key: "_id", Value: "doc"}}).Decode(&doc); err != nil {
		t.Fatalf("read doc: %v", err)
	}
	if doc.Count != 1 {
		t.Fatalf("count = %d; want 1", doc.Count)
	}
}

func TestRunOnceAbortsWhenWorkFails(t *testing.T) {
	s := testDatabase(t)
	probe := s.db.Collection("probe")
	insertDoc(t, s, "probe", bson.D{{Key: "_id", Value: "doc"}, {Key: "count", Value: 0}})
	err := s.runOnce(t.Context(), func(ctx context.Context) error {
		if _, err := probe.UpdateOne(ctx, bson.D{{Key: "_id", Value: "doc"}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "count", Value: 1}}}}); err != nil {
			return err
		}
		return apperr.ErrFailedPrecondition
	})
	if !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("runOnce = %v; want failed precondition", err)
	}
	var doc struct {
		Count int `bson:"count"`
	}
	if err := probe.FindOne(t.Context(), bson.D{{Key: "_id", Value: "doc"}}).Decode(&doc); err != nil {
		t.Fatalf("read doc: %v", err)
	}
	if doc.Count != 0 {
		t.Fatalf("count = %d; want 0 after the abort", doc.Count)
	}
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(conflictWait):
		t.Fatalf("timed out waiting for the %s", what)
		var zero T
		return zero
	}
}

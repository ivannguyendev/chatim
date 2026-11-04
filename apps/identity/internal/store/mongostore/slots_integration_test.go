package mongostore

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const loadPurpose = "directory-load"

func sameDatabase(t *testing.T, s *Store) *Store {
	t.Helper()
	other, err := Open(testOptions(itURI(t), s.db.Name()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(other.Close)
	return other
}

func tryLoadSlot(t *testing.T, s *Store, purpose string, slots int) (store.Slot, bool) {
	t.Helper()
	slot, ok, err := s.TryLoadSlot(t.Context(), purpose, slots)
	if err != nil {
		t.Fatalf("TryLoadSlot(%s, %d): %v", purpose, slots, err)
	}
	return slot, ok
}

func TestTryLoadSlotGivesEachSlotToOneHolder(t *testing.T) {
	a := testDatabase(t)
	b, c := sameDatabase(t, a), sameDatabase(t, a)
	if _, ok := tryLoadSlot(t, a, loadPurpose, 2); !ok {
		t.Fatal("first holder got no slot")
	}
	if _, ok := tryLoadSlot(t, b, loadPurpose, 2); !ok {
		t.Fatal("second holder got no slot")
	}
	if _, ok := tryLoadSlot(t, c, loadPurpose, 2); ok {
		t.Fatal("third holder got a slot; want none of 2")
	}
	var docs []bson.M
	if err := a.findAll(t.Context(), slotsCollection, bson.D{}, &docs); err != nil || len(docs) != 2 {
		t.Fatalf("slot docs = %v, %v; want 2", docs, err)
	}
	if docs[0]["holder"] == docs[1]["holder"] || docs[0]["purpose"] != loadPurpose {
		t.Fatalf("slot docs = %v; want two holders of %s", docs, loadPurpose)
	}
}

func TestTryLoadSlotTakesAnExpiredSlotAndTheOldHolderLosesItsRenew(t *testing.T) {
	a := testDatabase(t)
	b := sameDatabase(t, a)
	held, ok := tryLoadSlot(t, a, loadPurpose, 1)
	if !ok {
		t.Fatal("first holder got no slot")
	}
	if renewed, err := held.Renew(t.Context()); err != nil || !renewed {
		t.Fatalf("Renew while held = %v, %v; want true", renewed, err)
	}
	if _, ok := tryLoadSlot(t, b, loadPurpose, 1); ok {
		t.Fatal("second holder took a live slot")
	}
	past := bson.D{{Key: "$set", Value: bson.D{{Key: "expires_at", Value: time.Now().Add(-time.Hour)}}}}
	if _, err := a.db.Collection(slotsCollection).UpdateMany(t.Context(), bson.D{}, past); err != nil {
		t.Fatalf("expire slots: %v", err)
	}
	if _, ok := tryLoadSlot(t, b, loadPurpose, 1); !ok {
		t.Fatal("second holder could not take the expired slot")
	}
	if renewed, err := held.Renew(t.Context()); err != nil || renewed {
		t.Fatalf("Renew after losing the slot = %v, %v; want false", renewed, err)
	}
	held.Release()
	if _, ok := tryLoadSlot(t, a, loadPurpose, 1); ok {
		t.Fatal("old holder's Release freed the new holder's slot")
	}
}

func TestReleaseFreesTheSlotOnce(t *testing.T) {
	a := testDatabase(t)
	b := sameDatabase(t, a)
	held, ok := tryLoadSlot(t, a, loadPurpose, 1)
	if !ok {
		t.Fatal("first holder got no slot")
	}
	held.Release()
	held.Release()
	again, ok := tryLoadSlot(t, b, loadPurpose, 1)
	if !ok {
		t.Fatal("slot not free after Release")
	}
	held.Release()
	if renewed, err := again.Renew(t.Context()); err != nil || !renewed {
		t.Fatalf("new holder Renew = %v, %v; want true after the old holder's repeated Release", renewed, err)
	}
}

func TestTryLoadSlotPurposesDoNotCompete(t *testing.T) {
	a := testDatabase(t)
	b := sameDatabase(t, a)
	if _, ok := tryLoadSlot(t, a, loadPurpose, 1); !ok {
		t.Fatal("load slot not taken")
	}
	if _, ok := tryLoadSlot(t, b, "key-rotation", 1); !ok {
		t.Fatal("another purpose competed with the load slot")
	}
}

func TestTryLoadSlotRacingHoldersFillEachSlotOnce(t *testing.T) {
	a := testDatabase(t)
	const holders, slots = 6, 2
	stores := []*Store{a}
	for range holders - 1 {
		stores = append(stores, sameDatabase(t, a))
	}
	var wg sync.WaitGroup
	results := make(chan bool, holders)
	start := make(chan struct{})
	for _, s := range stores {
		wg.Go(func() {
			<-start
			_, ok, err := s.TryLoadSlot(t.Context(), loadPurpose, slots)
			if err != nil {
				t.Errorf("TryLoadSlot: %v", err)
			}
			results <- ok
		})
	}
	close(start)
	wg.Wait()
	close(results)
	got := 0
	for ok := range results {
		if ok {
			got++
		}
	}
	if got != slots {
		t.Fatalf("holders with a slot = %d; want %d", got, slots)
	}
}

func TestTryLoadSlotRefusesABadPurposeOrCount(t *testing.T) {
	s := testDatabase(t)
	for _, tc := range []struct {
		purpose string
		slots   int
	}{{"", 1}, {"load│x", 1}, {loadPurpose, 0}, {loadPurpose, -1}} {
		if _, _, err := s.TryLoadSlot(t.Context(), tc.purpose, tc.slots); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("TryLoadSlot(%q, %d) = %v; want invalid argument", tc.purpose, tc.slots, err)
		}
	}
}

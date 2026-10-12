package mongostore

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func twoOwners(t *testing.T, s *Store) (alice, bob domain.Member) {
	t.Helper()
	r := domain.Room{ID: itRoom, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 3}
	seats := []domain.Member{
		{Room: itRoom, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime},
		{Room: itRoom, Tenant: "acme", User: "bob", Role: domain.RoleOwner, JoinedAt: codecTime},
		{Room: itRoom, Tenant: "acme", User: "carol", Role: domain.RoleMember, JoinedAt: codecTime},
	}
	if err := s.Create(t.Context(), r, seats); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.MembersOf(t.Context(), itRoom, []string{"alice", "bob"})
	if err != nil || len(got) != 2 {
		t.Fatalf("MembersOf = %+v, %v", got, err)
	}
	return got[0], got[1]
}

func leave(m domain.Member) store.MemberWrite {
	return store.MemberWrite{Cur: m, Next: m.Next(m.Role, domain.MemberRemoved, m.Priority, "req-leave", m.User, codecTime.Add(time.Minute))}
}

func rawRoom(t *testing.T, db *mongo.Database) bson.Raw {
	t.Helper()
	raw, err := db.Collection(roomsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: int64(itRoom)}}).Raw()
	if err != nil {
		t.Fatalf("FindOne room: %v", err)
	}
	return raw
}

func TestOwnerTransactionWritesNothingWhenTheSecondWriteFails(t *testing.T) {
	s, db := itStore(t, itClient(t))
	alice, bob := twoOwners(t, s)
	stale := bob
	stale.Ver = 7
	_, err := s.ChangeOwners(t.Context(), itRoom, []string{"alice", "bob"}, func(store.OwnerView) ([]store.MemberWrite, error) {
		return []store.MemberWrite{leave(alice), leave(stale)}, nil
	})
	if !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("ChangeOwners = %v, want ErrRetryLater", err)
	}
	got, err := s.MembersOf(t.Context(), itRoom, []string{"alice", "bob"})
	if err != nil || len(got) != 2 || got[0].Ver != 1 || !got[0].Active() || got[1].Ver != 1 {
		t.Fatalf("MembersOf after the failed transaction = %+v, %v; want both untouched", got, err)
	}
	room := rawRoom(t, db)
	if _, ok := room.Lookup("owners_ver").Int64OK(); ok {
		t.Fatalf("room after the failed transaction = %s, want no owners_ver", room)
	}
	if n := room.Lookup("member_count").AsInt64(); n != 3 {
		t.Fatalf("member_count = %d, want 3", n)
	}
}

func TestTwoOwnerTransactionsOnOneRoomLetOnlyOneCommit(t *testing.T) {
	s, db := itStore(t, itClient(t))
	alice, bob := twoOwners(t, s)
	var ready sync.WaitGroup
	ready.Add(2)
	run := func(m domain.Member) error {
		_, err := s.ChangeOwners(t.Context(), itRoom, []string{m.User}, func(v store.OwnerView) ([]store.MemberWrite, error) {
			ready.Done()
			ready.Wait()
			if len(v.Owners) != 2 {
				return nil, errors.New("view lost an owner")
			}
			return []store.MemberWrite{leave(m)}, nil
		})
		return err
	}
	errs := make([]error, 2)
	var done sync.WaitGroup
	for i, m := range []domain.Member{alice, bob} {
		done.Go(func() { errs[i] = run(m) })
	}
	done.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, domain.ErrRetryLater):
			t.Fatalf("ChangeOwners = %v, want nil or ErrRetryLater", err)
		}
	}
	if won != 1 {
		t.Fatalf("committed transactions = %d (%v), want exactly 1", won, errs)
	}
	room := rawRoom(t, db)
	if n := room.Lookup("member_count").AsInt64(); n != 2 {
		t.Fatalf("member_count = %d, want 2 after one leave", n)
	}
	if v := room.Lookup("owners_ver").AsInt64(); v != 1 {
		t.Fatalf("owners_ver = %d, want 1", v)
	}
	if n, err := s.CountMembers(t.Context(), itRoom); err != nil || n != 2 {
		t.Fatalf("CountMembers = %d, %v; want 2", n, err)
	}
}

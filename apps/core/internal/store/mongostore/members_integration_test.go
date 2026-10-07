package mongostore

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const roomStateIndex = "room_id_1_state_1_role_1_priority_-1_joined_at_1_user_id_1"

func TestMongoMembersContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMembers(t, func(t *testing.T) storetest.MemberRooms {
		s, _ := itStore(t, client)
		return s
	})
}

func itRoomWithOwner(t *testing.T, s *Store) {
	t.Helper()
	r := domain.Room{ID: itRoom, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 1}
	if err := s.Create(t.Context(), r, []domain.Member{{Room: itRoom, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func rawMember(t *testing.T, db *mongo.Database, user string) bson.Raw {
	t.Helper()
	raw, err := db.Collection(membersCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: keys.Member(itRoom, user)}}).Raw()
	if err != nil {
		t.Fatalf("FindOne raw %q: %v", user, err)
	}
	return raw
}

func oplogEntriesOf(t *testing.T, db *mongo.Database, id []byte) int64 {
	t.Helper()
	filter := bson.D{
		{Key: "ns", Value: db.Name() + "." + membersCollection},
		{Key: "$or", Value: bson.A{bson.D{{Key: "o._id", Value: id}}, bson.D{{Key: "o2._id", Value: id}}}},
	}
	n, err := db.Client().Database("local").Collection("oplog.rs").CountDocuments(t.Context(), filter)
	if err != nil {
		t.Fatalf("count oplog entries: %v", err)
	}
	return n
}

func TestMemberDocumentLayoutAndNoOpAdd(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	want := slices.DeleteFunc(slices.Clone(memberFields), func(f string) bool { return f == "cleared_at" })
	if got := fieldNames(t, rawMember(t, db, "alice")); !slices.Equal(got, want) {
		t.Fatalf("created member fields = %v, want %v", got, want)
	}
	j := domain.Join{Room: itRoom, Tenant: "acme", RequestID: "req-add", By: "alice", At: codecTime, ReadSeq: 7}
	if res, err := s.AddMembers(t.Context(), j, []string{"carol"}); err != nil || res.Changed != 1 {
		t.Fatalf("AddMembers = %+v, %v; want 1 changed", res, err)
	}
	added := rawMember(t, db, "carol")
	if got := fieldNames(t, added); !slices.Equal(got, want) {
		t.Fatalf("added member fields = %v, want %v", got, want)
	}
	for _, f := range []string{"room_id", "state", "priority", "ver", "previous_state", "previous_priority", "read_seq", "read_ver"} {
		if typ := added.Lookup(f).Type; typ != bson.TypeInt64 {
			t.Fatalf("added %s type = %v, want int64", f, typ)
		}
	}
	entries := oplogEntriesOf(t, db, keys.Member(itRoom, "carol"))
	again := j
	again.RequestID, again.By, again.At = "req-again", "bob", codecTime.Add(time.Minute)
	res, err := s.AddMembers(t.Context(), again, []string{"carol"})
	if err != nil || res.Changed != 0 || res.Members[0].Ver != 1 || !res.Members[0].UpdatedAt.Equal(codecTime) {
		t.Fatalf("AddMembers again = %+v, %v; want nothing changed at ver 1", res, err)
	}
	if after := rawMember(t, db, "carol"); !bytes.Equal(after, added) {
		t.Fatalf("add of an active member changed the doc: %s -> %s", added, after)
	}
	if n := oplogEntriesOf(t, db, keys.Member(itRoom, "carol")); n != entries {
		t.Fatalf("oplog entries of carol = %d after a no-op add, want %d", n, entries)
	}
}

func TestMemberCountIsCoveredByTheRoomStateIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	count := bson.D{{Key: "count", Value: membersCollection}, {Key: "query", Value: bson.D{{Key: "room_id", Value: int64(itRoom)}, {Key: "state", Value: int64(domain.MemberActive)}}}}
	var out bson.Raw
	err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: count}, {Key: "verbosity", Value: "queryPlanner"}}).Decode(&out)
	if err != nil {
		t.Fatalf("explain count: %v", err)
	}
	plan := out.Lookup("queryPlanner", "winningPlan").String()
	if !strings.Contains(plan, roomStateIndex) || !strings.Contains(plan, "COUNT_SCAN") || strings.Contains(plan, "FETCH") {
		t.Fatalf("winning plan = %s, want a COUNT_SCAN on %s without FETCH", plan, roomStateIndex)
	}
}

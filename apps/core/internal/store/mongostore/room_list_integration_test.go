package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoRoomListContract(t *testing.T) {
	client := itClient(t)
	storetest.RunRoomList(t, func(t *testing.T) storetest.ListableRooms {
		s, _ := itStore(t, client)
		return s
	})
}

func TestMongoRoomTailsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunRoomTails(t, func(t *testing.T) storetest.TailMessages {
		s, _ := itStore(t, client)
		return s
	})
}

func itListRooms(t *testing.T, s *Store, n int) {
	t.Helper()
	var acts []store.Activity
	for i := range n {
		id := itRoom + uint64(i)
		r := domain.Room{ID: id, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 1}
		if err := s.Create(t.Context(), r, []domain.Member{{Room: id, Tenant: "acme", User: "minh", Role: domain.RoleOwner, JoinedAt: codecTime}}); err != nil {
			t.Fatalf("Create(%d): %v", id, err)
		}
		acts = append(acts, store.Activity{Room: id, Seq: 1, At: codecTime.Add(time.Duration(i) * time.Second)})
	}
	if err := s.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
}

func explainRaw(t *testing.T, db *mongo.Database, cmd bson.D, verbosity string) bson.Raw {
	t.Helper()
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: cmd}, {Key: "verbosity", Value: verbosity}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	return raw
}

func TestCountRoomsIsACountScanOfTheUserRoomsIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itListRooms(t, s, 3)
	count := bson.D{{Key: "count", Value: membersCollection}, {Key: "query", Value: memberRoomsFilter("acme", "minh")}, {Key: "limit", Value: 2}}
	stages, indexes := winningPlan(t, db, count)
	if !slices.Contains(stages, "COUNT_SCAN") || slices.Contains(stages, "FETCH") || !slices.Equal(indexes, []string{membersUserIndex}) {
		t.Fatalf("winning plan stages %v on %v, want a COUNT_SCAN of %s without FETCH", stages, indexes, membersUserIndex)
	}
}

func TestStepwiseMembersReadIsCoveredByTheUserRoomsIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itListRooms(t, s, 3)
	find := bson.D{{Key: "find", Value: membersCollection}, {Key: "filter", Value: memberRoomsFilter("acme", "minh")}, {Key: "projection", Value: memberOrderFields}}
	stages, indexes := winningPlan(t, db, find)
	if slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") || !slices.Equal(indexes, []string{membersUserIndex}) {
		t.Fatalf("winning plan stages %v on %v, want a covered scan of %s", stages, indexes, membersUserIndex)
	}
}

func TestJoinedRoomOrderLooksUpRoomsByID(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itListRooms(t, s, 3)
	q := store.RoomListQuery{Tenant: "acme", User: "minh", Folder: domain.FolderMain}
	aggregate := bson.D{{Key: "aggregate", Value: membersCollection}, {Key: "pipeline", Value: joinedRoomPipeline(q)}, {Key: "cursor", Value: bson.D{}}}
	raw := explainRaw(t, db, aggregate, "executionStats")
	lookup, ok := lookupStage(raw)
	if !ok {
		t.Fatalf("explain has no $lookup stage: %s", raw)
	}
	indexes, _ := lookup.Lookup("indexesUsed").ArrayOK()
	used, _ := indexes.Values()
	scans, _ := lookup.Lookup("collectionScans").AsInt64OK()
	examined, _ := lookup.Lookup("totalDocsExamined").AsInt64OK()
	if len(used) != 1 || used[0].StringValue() != "_id_" || scans != 0 || examined != 3 {
		t.Fatalf("$lookup used %v with %d collection scans and %d docs examined, want only _id_, none and 3", used, scans, examined)
	}
}

func lookupStage(raw bson.Raw) (bson.Raw, bool) {
	stages, ok := raw.Lookup("stages").ArrayOK()
	if !ok {
		return nil, false
	}
	vals, _ := stages.Values()
	for _, v := range vals {
		if d, ok := v.DocumentOK(); ok {
			if _, err := d.LookupErr("$lookup"); err == nil {
				return d, true
			}
		}
	}
	return nil, false
}

func TestTailWindowIsABoundedClusteredScan(t *testing.T) {
	s, db := itStore(t, itClient(t))
	seedTimeline(t, s, itRoom, 0, 400)
	seedTimeline(t, s, itRoom, 9, 50)
	seedTimeline(t, s, itRoom+1, 0, 50)
	for _, after := range []uint64{0, 350} {
		q := tailRange(itRoom, after, domain.UnreadWindow)
		find := bson.D{{Key: "find", Value: messagesCollection}, {Key: "filter", Value: q.filter}, {Key: "sort", Value: q.sort()}, {Key: "limit", Value: q.limit}, {Key: "projection", Value: tailFields}}
		raw := explainRaw(t, db, find, "executionStats")
		var names []string
		walkStages(raw.Lookup("queryPlanner", "winningPlan"), func(d bson.Raw) { names = append(names, d.Lookup("stage").StringValue()) })
		if !slices.Contains(names, "CLUSTERED_IXSCAN") || slices.Contains(names, "SORT") || slices.Contains(names, "COLLSCAN") {
			t.Fatalf("tail after %d stages = %v, want one CLUSTERED_IXSCAN and no SORT or COLLSCAN", after, names)
		}
		want := int64(min(400-after, domain.UnreadWindow))
		examined, _ := raw.Lookup("executionStats", "totalDocsExamined").AsInt64OK()
		returned, _ := raw.Lookup("executionStats", "nReturned").AsInt64OK()
		if returned != want || examined > want+1 {
			t.Fatalf("tail after %d returned %d examined %d, want %d returned and at most %d examined", after, returned, examined, want, want+1)
		}
	}
}

func TestHiddenInRoomsScansTheHiddenIndex(t *testing.T) {
	_, db := itStore(t, itClient(t))
	ranges := []store.HiddenRange{{Room: itRoom, From: 1, To: 300}, {Room: itRoom + 1, From: 40, To: 90}}
	filter, err := hiddenRoomsFilter("minh", ranges)
	if err != nil {
		t.Fatalf("hiddenRoomsFilter: %v", err)
	}
	find := bson.D{{Key: "find", Value: hiddenCollection}, {Key: "filter", Value: filter}, {Key: "projection", Value: hiddenRoomFields}}
	stages, indexes := winningPlan(t, db, find)
	const index = "user_id_1_room_id_1_thread_root_1_seq_1"
	if slices.Contains(stages, "COLLSCAN") || slices.Contains(stages, "FETCH") || len(indexes) != len(ranges) {
		t.Fatalf("winning plan stages %v on %v, want %d covered scans of %s", stages, indexes, len(ranges), index)
	}
	for _, ix := range indexes {
		if ix != index {
			t.Fatalf("winning plan uses %s, want only %s", ix, index)
		}
	}
}

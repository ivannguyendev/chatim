package mongostore

import (
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoDirectRoomsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunDirectRooms(t, func(t *testing.T) store.DirectRooms {
		s, _ := itStore(t, client)
		return s.DirectRooms()
	})
}

func TestBootstrapCreatesRoomDMsWithOnlyTheClusteredID(t *testing.T) {
	_, db := itStore(t, itClient(t))
	assertClusteredLayout(t, db, roomDMsCollection)
	for k := range indexKeys(t, db.Collection(roomDMsCollection)) {
		if k != "_id:1" {
			t.Fatalf("room_dms has index %s, want only the clustered _id", k)
		}
	}
}

func TestDirectRoomDocAndRoomDMKeyOnDisk(t *testing.T) {
	s, db := itStore(t, itClient(t))
	if _, err := s.DirectRooms().Claim(t.Context(), "acme", "minh", "lan", 8812, codecTime); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	var claim bson.M
	if err := db.Collection(roomDMsCollection).FindOne(t.Context(), bson.D{}).Decode(&claim); err != nil {
		t.Fatalf("read room_dms: %v", err)
	}
	if claim["_id"] != "acme│lan│minh" || claim["room_id"] != int64(8812) || len(claim) != 3 {
		t.Fatalf("room_dms doc = %v, want _id acme│lan│minh, room_id 8812, created_at", claim)
	}
	r := domain.Room{ID: 8812, Tenant: "acme", Type: domain.RoomDM, CreatedBy: "minh", CreatedAt: codecTime, MemberCount: 2, DMKey: domain.DirectKey("acme", "lan", "minh")}
	if err := s.InsertRoom(t.Context(), r); err != nil {
		t.Fatalf("InsertRoom: %v", err)
	}
	var room bson.M
	if err := db.Collection(roomsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: int64(8812)}}).Decode(&room); err != nil {
		t.Fatalf("read room: %v", err)
	}
	_, hasVer := room["member_count_ver"]
	if room["dm_key"] != "acme│lan│minh" || fmt.Sprint(room["member_count"]) != "0" || hasVer {
		t.Fatalf("room doc = %v, want dm_key, member_count 0 and no member_count_ver", room)
	}
}

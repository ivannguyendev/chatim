package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/modules/media/internal/store"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

func TestRoomAvatarsContractOnMongo(t *testing.T) {
	storetest.RunRoomAvatars(t, func(t *testing.T) store.RoomAvatars {
		return NewRoomAvatars(itBootstrapped(t, itDatabase(t)))
	})
}

func TestInUseUsesTheFileIndex(t *testing.T) {
	db := itBootstrapped(t, itDatabase(t))
	s := NewRoomAvatars(db)
	for i := range 20 {
		room := storetest.RoomAvatar(string(rune('a'+i)), "acme", "acme/an/pub/20261113/"+string(rune('a'+i)), 1)
		if err := s.Put(t.Context(), room, 0); err != nil {
			t.Fatal(err)
		}
	}
	index, err := mongoindex.Name(mongo.IndexModel{Keys: avatarsByFile})
	if err != nil {
		t.Fatal(err)
	}
	find := bson.D{
		{Key: "find", Value: roomAvatarsCollection}, {Key: "filter", Value: bson.D{{Key: "file_key", Value: "acme/an/pub/20261113/c"}}},
		{Key: "projection", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "limit", Value: 1}, {Key: "hint", Value: avatarsByFile},
	}
	stages, indexes := winningStages(t, db, find)
	if !slices.Contains(stages, "IXSCAN") || !slices.Contains(indexes, index) || slices.Contains(stages, "COLLSCAN") {
		t.Fatalf("stages %v on %v, want IXSCAN on %s without COLLSCAN", stages, indexes, index)
	}
	if used, err := s.InUse(t.Context(), "acme/an/pub/20261113/c"); err != nil || !used {
		t.Fatalf("InUse = %v, %v", used, err)
	}
}

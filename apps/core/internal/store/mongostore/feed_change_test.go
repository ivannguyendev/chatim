package mongostore

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func changeOn(t *testing.T, coll string, doc any) changeDoc {
	t.Helper()
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return changeDoc{WallTime: codecTime, NS: changeNS{Coll: coll}, FullDocument: raw}
}

func TestDecodeChangeReadsMessagesAndRooms(t *testing.T) {
	m := sampleMessage()
	mdoc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	got, err := decodeChange(changeOn(t, messagesCollection, mdoc))
	if err != nil || got.Kind != store.MessageInserted || store.KeyOf(got.Msg) != store.KeyOf(m) || got.Msg.CID != m.CID ||
		!got.CommittedAt.Equal(codecTime) || got.Room.ID != 0 {
		t.Fatalf("message change = %+v, %v", got, err)
	}
	r := domain.Room{ID: 7_340_000_009, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 2}
	rdoc, err := encodeRoom(r)
	if err != nil {
		t.Fatalf("encodeRoom: %v", err)
	}
	got, err = decodeChange(changeOn(t, roomsCollection, rdoc))
	if err != nil || got.Kind != store.RoomInserted || got.Room.ID != r.ID || got.Room.Name != "Team" || got.Room.MemberCount != 2 ||
		!got.Room.CreatedAt.Equal(codecTime) || got.Msg.Room != 0 {
		t.Fatalf("room change = %+v, %v", got, err)
	}
}

func TestDecodeChangeRejectsOtherCollectionsAndBrokenDocuments(t *testing.T) {
	cases := map[string]changeDoc{
		"members insert":   changeOn(t, membersCollection, bson.D{{Key: "user_id", Value: "bob"}}),
		"bad message id":   changeOn(t, messagesCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"negative room id": changeOn(t, roomsCollection, bson.D{{Key: "_id", Value: int64(-1)}}),
		"bad edit id":      changeOn(t, editsCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"no document":      {WallTime: codecTime, NS: changeNS{Coll: messagesCollection}},
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
}

func TestDecodeChangeReadsEdits(t *testing.T) {
	e := sampleEdit()
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	got, err := decodeChange(changeOn(t, editsCollection, doc))
	if err != nil || got.Kind != store.EditInserted || store.EditKeyOf(got.Edit) != store.EditKeyOf(e) || got.Edit.Version != e.Version ||
		got.Edit.Text != e.Text || got.Edit.Prev != e.Prev || !got.CommittedAt.Equal(codecTime) || got.Msg.Room != 0 || got.Room.ID != 0 {
		t.Fatalf("edit change = %+v, %v", got, err)
	}
}

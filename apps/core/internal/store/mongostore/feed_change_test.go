package mongostore

import (
	"errors"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
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
		"other collection": changeOn(t, reconcilerStateCollection, bson.D{{Key: "_id", Value: "changes"}}),
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
		got.Edit.Text != e.Text || !got.CommittedAt.Equal(codecTime) || got.Msg.Room != 0 || got.Room.ID != 0 {
		t.Fatalf("edit change = %+v, %v", got, err)
	}
}

func TestFeedPipelineLetsOnlyReactionAndMemberChangesThrough(t *testing.T) {
	b, err := bson.Marshal(feedPipeline()[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	branches, err := bson.Raw(b).Lookup("$match", "$or").Array().Values()
	if err != nil || len(branches) != 4 {
		t.Fatalf("$or = %v, %v; want 4 branches", branches, err)
	}
	inserts, _ := branches[0].Document().Lookup("ns.coll", "$in").Array().Values()
	var colls []string
	for _, v := range inserts {
		colls = append(colls, v.StringValue())
	}
	want := []string{messagesCollection, roomsCollection, editsCollection, interactionsCollection, pinActionsCollection, membersCollection, hiddenCollection}
	if op := branches[0].Document().Lookup("operationType").StringValue(); op != "insert" || !slices.Equal(colls, want) {
		t.Fatalf("insert branch = %s, want inserts of %v", branches[0], want)
	}
	if coll := branches[1].Document().Lookup("ns.coll").StringValue(); coll != interactionsCollection {
		t.Fatalf("interaction branch = %s, want updates and replaces of message_interactions", branches[1])
	}
	replaces := branches[2].Document()
	if replaces.Lookup("operationType").StringValue() != "replace" || replaces.Lookup("ns.coll").StringValue() != membersCollection {
		t.Fatalf("member replace branch = %s", replaces)
	}
	updates := branches[3].Document()
	fields, _ := updates.Lookup("$or").Array().Values()
	var names []string
	for _, f := range fields {
		elems, _ := f.Document().Elements()
		names = append(names, elems[0].Key())
	}
	wantFields := []string{"updateDescription.updatedFields.ver", "updateDescription.updatedFields.read_ver", "updateDescription.updatedFields.cleared_at"}
	if updates.Lookup("operationType").StringValue() != "update" || updates.Lookup("ns.coll").StringValue() != membersCollection || !slices.Equal(names, wantFields) {
		t.Fatalf("member update branch = %s, want updates of members that touch %v", updates, wantFields)
	}
}

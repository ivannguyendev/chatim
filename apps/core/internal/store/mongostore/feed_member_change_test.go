package mongostore

import (
	"errors"
	"math"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func memberUpdate(t *testing.T, id any, fields bson.D) changeDoc {
	t.Helper()
	ev := reactionUpdate(t, id, fields)
	ev.NS.Coll = membersCollection
	return ev
}

func TestDecodeChangeReadsMemberUpdatesFromTheKeyAndVer(t *testing.T) {
	id := keys.Member(7_340_000_001, "bob")
	want := domain.Member{Room: 7_340_000_001, User: "bob", Ver: 4}
	for name, v := range map[string]any{"int32": int32(4), "int64": int64(4), "double": float64(4)} {
		got, err := decodeChange(memberUpdate(t, id, bson.D{{Key: "state", Value: int64(2)}, {Key: "ver", Value: v}, {Key: "read_ver", Value: int64(9)}}))
		if err != nil || got.Kind != store.MemberChanged || got.Member != want || !got.CommittedAt.Equal(codecTime) || got.Reaction.N != 0 {
			t.Fatalf("%s: update change = %+v, %v; want %+v", name, got, err, want)
		}
	}
}

func TestDecodeChangeReadsMemberInsertsAndReplacesFromTheDocument(t *testing.T) {
	m := sampleMember()
	doc, err := encodeMember(m)
	if err != nil {
		t.Fatalf("encodeMember: %v", err)
	}
	for _, op := range []string{"insert", "replace"} {
		ev := changeOn(t, membersCollection, doc)
		ev.OperationType = op
		got, err := decodeChange(ev)
		if err != nil || got.Kind != store.MemberChanged || !sameMemberTimes(got.Member, m) {
			t.Fatalf("%s change = %+v, %v; want %+v", op, got, err, m)
		}
		got.Member.JoinedAt, got.Member.ClearedAt, got.Member.UpdatedAt, got.Member.LastChangeAt = m.JoinedAt, m.ClearedAt, m.UpdatedAt, m.LastChangeAt
		if got.Member != m {
			t.Fatalf("%s change member = %+v, want %+v", op, got.Member, m)
		}
	}
}

func TestDecodeChangeReadsReadPositionUpdates(t *testing.T) {
	id := keys.Member(7_340_000_001, "bob")
	got, err := decodeChange(memberUpdate(t, id, bson.D{{Key: "read_seq", Value: int64(30)}, {Key: "read_ver", Value: int64(7)}, {Key: "last_change_at", Value: codecTime}}))
	if want := (domain.Member{Room: 7_340_000_001, User: "bob", ReadVer: 7}); err != nil || got.Kind != store.ReadChanged || got.Member != want {
		t.Fatalf("read change = %+v, %v; want %+v", got, err, want)
	}
	got, err = decodeChange(memberUpdate(t, id, bson.D{{Key: "cleared_at", Value: codecTime}, {Key: "last_change_at", Value: codecTime}}))
	if want := (domain.Member{Room: 7_340_000_001, User: "bob"}); err != nil || got.Kind != store.HistoryCleared || got.Member != want {
		t.Fatalf("clear change = %+v, %v; want %+v", got, err, want)
	}
}

func TestDecodeChangeReadsHiddenInserts(t *testing.T) {
	doc := hiddenDoc{User: "bob", Room: 7_340_000_001, Thread: 3, Seq: 9, CreatedAt: codecTime}
	got, err := decodeChange(changeOn(t, hiddenCollection, doc))
	want := domain.HiddenMessage{User: "bob", Room: 7_340_000_001, Thread: 3, Seq: 9, At: codecTime}
	if err != nil || got.Kind != store.MessageHidden || !got.Hidden.At.Equal(codecTime) || got.Member.User != "" {
		t.Fatalf("hide change = %+v, %v; want %+v", got, err, want)
	}
	if got.Hidden.At = want.At; got.Hidden != want {
		t.Fatalf("hide change = %+v, want %+v", got.Hidden, want)
	}
}

func TestDecodeChangeRejectsBrokenMemberChanges(t *testing.T) {
	id := keys.Member(7_340_000_001, "bob")
	bad := sampleMember()
	bad.User = "a.b"
	badDoc, _ := encodeMember(bad)
	cases := map[string]changeDoc{
		"update with nothing known":   memberUpdate(t, id, bson.D{{Key: "last_change_at", Value: codecTime}}),
		"update with ver 0":           memberUpdate(t, id, bson.D{{Key: "ver", Value: int32(0)}}),
		"update with ver too big":     memberUpdate(t, id, bson.D{{Key: "ver", Value: int64(math.MaxUint32) + 1}}),
		"update with string ver":      memberUpdate(t, id, bson.D{{Key: "ver", Value: "2"}}),
		"update with read ver 0":      memberUpdate(t, id, bson.D{{Key: "read_ver", Value: int64(0)}}),
		"update with negative read":   memberUpdate(t, id, bson.D{{Key: "read_ver", Value: int64(-1)}}),
		"update of a short key":       memberUpdate(t, keys.Msg(1, 0, 1)[:8], bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a bad user":        memberUpdate(t, keys.Member(1, "a.b"), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of room 0":            memberUpdate(t, keys.Member(0, "bob"), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a string key":      memberUpdate(t, "x", bson.D{{Key: "ver", Value: int32(2)}}),
		"insert of a bad user":        changeOn(t, membersCollection, badDoc),
		"insert of a bad state":       changeOn(t, membersCollection, bson.D{{Key: "user_id", Value: "bob"}, {Key: "state", Value: int64(7)}}),
		"hidden insert without a key": changeOn(t, hiddenCollection, bson.D{{Key: "user_id", Value: "bob"}}),
		"hidden insert of a bad user": changeOn(t, hiddenCollection, hiddenDoc{User: "a b", Room: 1, Seq: 1, CreatedAt: codecTime}),
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
}

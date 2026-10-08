package mongostore

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var memberFields = []string{
	"_id", "room_id", "tenant", "user_id", "role", "state", "priority", "joined_at", "ver",
	"previous_role", "previous_state", "previous_priority", "request_id", "updated_at", "updated_by",
	"last_change_at", "cleared_at", "read_seq", "read_ver",
}

func sampleMember() domain.Member {
	return domain.Member{
		Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleAdmin, JoinedAt: codecTime,
		ClearedAt: codecTime.Add(time.Minute), State: domain.MemberActive, Ver: 3, Priority: -4,
		PreviousRole: domain.RoleMember, PreviousState: domain.MemberRemoved, PreviousPriority: 9,
		RequestID: "req-1", UpdatedAt: codecTime.Add(2 * time.Minute), UpdatedBy: "alice",
		LastChangeAt: codecTime.Add(3 * time.Minute), ReadSeq: 42, ReadVer: 5,
	}
}

func TestMemberCodecRoundTrip(t *testing.T) {
	m := sampleMember()
	doc, err := encodeMember(m)
	if err != nil {
		t.Fatalf("encodeMember: %v", err)
	}
	if !bytes.Equal(doc.ID, keys.Member(m.Room, m.User)) {
		t.Fatalf("_id = %x, want keys.Member", doc.ID)
	}
	back, raw := roundTrip(t, doc)
	if got := fieldNames(t, raw); !slices.Equal(got, memberFields) {
		t.Fatalf("fields = %v, want %v", got, memberFields)
	}
	for _, f := range []string{"room_id", "state", "priority", "ver", "previous_state", "previous_priority", "read_seq", "read_ver"} {
		if typ := raw.Lookup(f).Type; typ != bson.TypeInt64 {
			t.Fatalf("%s type = %v, want int64", f, typ)
		}
	}
	got, err := decodeMember(back)
	if err != nil || !sameMemberTimes(got, m) {
		t.Fatalf("decodeMember = %+v, %v; want %+v", got, err, m)
	}
	got.JoinedAt, got.ClearedAt, got.UpdatedAt, got.LastChangeAt = m.JoinedAt, m.ClearedAt, m.UpdatedAt, m.LastChangeAt
	if got != m {
		t.Fatalf("decoded %+v, want %+v", got, m)
	}
	m.ClearedAt = time.Time{}
	doc, _ = encodeMember(m)
	if _, raw := roundTrip(t, doc); slices.Contains(fieldNames(t, raw), "cleared_at") {
		t.Fatal("cleared_at written for a member that never cleared")
	}
}

func sameMemberTimes(a, b domain.Member) bool {
	return a.JoinedAt.Equal(b.JoinedAt) && a.ClearedAt.Equal(b.ClearedAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.LastChangeAt.Equal(b.LastChangeAt)
}

func TestMemberCodecRejectsOutOfRangeValues(t *testing.T) {
	good, err := encodeMember(sampleMember())
	if err != nil {
		t.Fatalf("encodeMember: %v", err)
	}
	cases := map[string]func(*memberDoc){
		"negative room":        func(d *memberDoc) { d.Room = -1 },
		"negative ver":         func(d *memberDoc) { d.Ver = -1 },
		"ver above max uint32": func(d *memberDoc) { d.Ver = math.MaxUint32 + 1 },
		"state zero":           func(d *memberDoc) { d.State = 0 },
		"state three":          func(d *memberDoc) { d.State = 3 },
		"previous state three": func(d *memberDoc) { d.PreviousState = 3 },
		"negative previous":    func(d *memberDoc) { d.PreviousState = -1 },
		"priority above int32": func(d *memberDoc) { d.Priority = math.MaxInt32 + 1 },
		"previous below int32": func(d *memberDoc) { d.PreviousPriority = math.MinInt32 - 1 },
		"negative read seq":    func(d *memberDoc) { d.ReadSeq = -1 },
		"negative read ver":    func(d *memberDoc) { d.ReadVer = -1 },
	}
	for name, mutate := range cases {
		d := good
		mutate(&d)
		if _, err := decodeMember(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeMember = %v, want errCorrupt", name, err)
		}
	}
	for name, m := range map[string]domain.Member{
		"room above max int64":     func() domain.Member { m := sampleMember(); m.Room = math.MaxInt64 + 1; return m }(),
		"read seq above max int64": func() domain.Member { m := sampleMember(); m.ReadSeq = math.MaxInt64 + 1; return m }(),
		"read ver above max int64": func() domain.Member { m := sampleMember(); m.ReadVer = math.MaxInt64 + 1; return m }(),
	} {
		if _, err := encodeMember(m); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: encodeMember = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestRoomIDAboveMaxInt64IsRejected(t *testing.T) {
	r := domain.Room{ID: math.MaxInt64 + 1, Tenant: "acme", Type: domain.RoomDM}
	if _, err := encodeRoom(r); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("encodeRoom error = %v, want ErrInvalidArgument", err)
	}
	if _, err := decodeRoom(roomDoc{ID: -1}); err == nil {
		t.Fatal("decodeRoom accepted a negative id")
	}
	if _, err := decodeRoom(roomDoc{ID: 1, MemberCountVer: -1}); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeRoom(negative member count ver) = %v, want errCorrupt", err)
	}
}

func TestJoinPipelineKeepsActiveDocsAndTakesStringsLiterally(t *testing.T) {
	j := domain.Join{Room: 7_340_000_001, Tenant: "$acme", RequestID: "$req", By: "$by", At: codecTime, ReadSeq: 30}
	p := joinPipeline(j, 7_340_000_001, 30, "$x")
	if len(p) != 1 || len(p[0]) != 1 || p[0][0].Key != "$set" {
		t.Fatalf("pipeline = %v, want one $set stage", p)
	}
	set := p[0][0].Value.(bson.D)
	names := make([]string, len(set))
	for i, e := range set {
		names[i] = e.Key
	}
	want := slices.DeleteFunc(slices.Clone(memberFields[1:]), func(f string) bool { return f == "cleared_at" })
	if !slices.Equal(names, want) {
		t.Fatalf("$set fields = %v, want %v", names, want)
	}
	direct := map[string]any{"room_id": int64(7_340_000_001), "tenant": literal("$acme"), "user_id": literal("$x"), "state": int64(domain.MemberActive)}
	for _, e := range set {
		if v, ok := direct[e.Key]; ok {
			if !equalBSON(t, e.Value, v) {
				t.Fatalf("%s = %v, want %v", e.Key, e.Value, v)
			}
			continue
		}
		cond, ok := e.Value.(bson.D)
		if !ok || len(cond) != 1 || cond[0].Key != "$cond" {
			t.Fatalf("%s = %v, want a $cond", e.Key, e.Value)
		}
		args := cond[0].Value.(bson.A)
		if !equalBSON(t, args[0], activeMember()) || args[1] != "$"+e.Key {
			t.Fatalf("%s $cond = %v, want to keep $%s of an active doc", e.Key, args, e.Key)
		}
	}
	b, err := bson.Marshal(set)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := bson.Raw(b)
	for _, f := range []string{"request_id", "updated_by"} {
		if v := raw.Lookup(f, "$cond", "2", "$literal"); v.StringValue() != "$"+map[string]string{"request_id": "req", "updated_by": "by"}[f] {
			t.Fatalf("%s new value = %v, want a $literal", f, raw.Lookup(f))
		}
	}
}

func equalBSON(t *testing.T, a, b any) bool {
	t.Helper()
	ra, err := bson.Marshal(bson.D{{Key: "v", Value: a}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	rb, err := bson.Marshal(bson.D{{Key: "v", Value: b}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return bytes.Equal(ra, rb)
}

package dedupe

import (
	"testing"
	"time"
)

func TestKeyNamesRoomUserAndCID(t *testing.T) {
	k := Key{Room: 18446744073709551615, User: "Alice_1", CID: "c-9"}
	if got := k.String(); got != "chatim:cid:18446744073709551615:Alice_1:c-9" {
		t.Fatalf("key = %q", got)
	}
}

func TestValuesRoundTripStrictly(t *testing.T) {
	records := []Record{
		sampleRecord,
		{Seq: 1, Pts: 1, CreatedAt: time.UnixMilli(0).UTC()},
		{Seq: 18446744073709551614, Pts: 3, CreatedAt: time.UnixMilli(-5).UTC()},
	}
	for _, r := range records {
		v := committedValue(r)
		got, ok := parseValue(v, "core-a")
		if !ok || got.Status != Committed || !sameRecord(got.Record, r) {
			t.Fatalf("parse(%q) = %+v, %v; want committed %+v", v, got, ok, r)
		}
		if got.Record.CreatedAt.Location() != time.UTC {
			t.Fatalf("parsed time %v is not UTC", got.Record.CreatedAt)
		}
	}
	pending := map[string]Status{"p:core-a": PendingHere, "p:core-b": PendingElsewhere, "p:core-a.svc": PendingElsewhere}
	for v, want := range pending {
		if got, ok := parseValue(v, "core-a"); !ok || got.Status != want {
			t.Fatalf("parse(%q) = %+v, %v; want %v", v, got, ok, want)
		}
	}
}

func TestStatusNames(t *testing.T) {
	want := map[Status]string{Absent: "absent", Reserved: "reserved", Committed: "committed", PendingHere: "pending-here", PendingElsewhere: "pending-elsewhere"}
	for s, name := range want {
		if s.String() != name {
			t.Errorf("Status(%d) = %q, want %q", int(s), s.String(), name)
		}
	}
}

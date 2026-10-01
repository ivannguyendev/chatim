package mongostore

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const itRoom uint64 = 7_340_000_001

func msgAt(room, thread, seq uint64) domain.Message {
	m := sampleMessage()
	m.Room, m.Thread, m.Seq, m.Pts, m.CID = room, thread, seq, seq, fmt.Sprintf("cid-%d", seq)
	return m
}

func seedTimeline(t *testing.T, s *Store, room, thread, n uint64) {
	t.Helper()
	batch := make([]domain.Message, 0, n)
	for seq := uint64(1); seq <= n; seq++ {
		batch = append(batch, msgAt(room, thread, seq))
	}
	for i, r := range s.Insert(t.Context(), batch) {
		if r.Outcome != store.Inserted {
			t.Fatalf("seed %d/%d/%d = %v (%v)", room, thread, batch[i].Seq, r.Outcome, r.Err)
		}
	}
}

func walkStages(v bson.RawValue, visit func(bson.Raw)) {
	if d, ok := v.DocumentOK(); ok {
		if _, isStage := d.Lookup("stage").StringValueOK(); isStage {
			visit(d)
		}
		elems, _ := d.Elements()
		for _, e := range elems {
			walkStages(e.Value(), visit)
		}
		return
	}
	if a, ok := v.ArrayOK(); ok {
		vals, _ := a.Values()
		for _, x := range vals {
			walkStages(x, visit)
		}
	}
}

func explainPage(t *testing.T, db *mongo.Database, q store.PageQuery) bson.Raw {
	t.Helper()
	mq := pageRange(q)
	find := bson.D{{Key: "find", Value: messagesCollection}, {Key: "filter", Value: mq.filter}, {Key: "sort", Value: mq.sort()}, {Key: "limit", Value: mq.limit}}
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: find}, {Key: "verbosity", Value: "executionStats"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	return raw
}

func assertRecordBound(t *testing.T, scan bson.Raw, field string, want []byte) {
	t.Helper()
	_, got, ok := scan.Lookup(field).BinaryOK()
	if !ok || !bytes.Equal(got, want) {
		t.Fatalf("%s = %x, want %x; scan %s", field, got, want, scan)
	}
}

func TestPageExplainIsBoundedClusteredScan(t *testing.T) {
	s, db := itStore(t, itClient(t))
	seedTimeline(t, s, itRoom-1, 0, 50)
	seedTimeline(t, s, itRoom, 0, 300)
	seedTimeline(t, s, itRoom, 9, 50)
	seedTimeline(t, s, itRoom+1, 0, 300)
	const limit = 20
	start, end := keys.MsgRange(itRoom, 0, 0, math.MaxUint64)
	mid := keys.Msg(itRoom, 0, 150)
	tests := []struct {
		name   string
		anchor store.Anchor
		lo, hi []byte
	}{
		{"latest", store.Latest, start, end},
		{"oldest", store.Oldest, start, end},
		{"before 150", store.Before, start, mid},
		{"after 150", store.After, mid, end},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := explainPage(t, db, store.PageQuery{Room: itRoom, Anchor: tt.anchor, Seq: 150, Limit: limit})
			var names []string
			var scans []bson.Raw
			walkStages(raw.Lookup("queryPlanner", "winningPlan"), func(d bson.Raw) {
				names = append(names, d.Lookup("stage").StringValue())
				if names[len(names)-1] == "CLUSTERED_IXSCAN" {
					scans = append(scans, d)
				}
			})
			if len(scans) != 1 || slices.Contains(names, "SORT") || slices.Contains(names, "COLLSCAN") {
				t.Fatalf("winning plan stages = %v, want one CLUSTERED_IXSCAN and no SORT or COLLSCAN", names)
			}
			assertRecordBound(t, scans[0], "minRecord", tt.lo)
			assertRecordBound(t, scans[0], "maxRecord", tt.hi)
			examined, _ := raw.Lookup("executionStats", "totalDocsExamined").AsInt64OK()
			returned, _ := raw.Lookup("executionStats", "nReturned").AsInt64OK()
			if returned != limit || examined > limit+1 {
				t.Fatalf("returned %d examined %d, want %d returned and at most %d examined", returned, examined, limit, limit+1)
			}
		})
	}
}

func TestInsertMapsMixedBatch(t *testing.T) {
	s, db := itStore(t, itClient(t))
	existing := msgAt(itRoom, 0, 1)
	seedTimeline(t, s, itRoom, 0, 1)
	again, twin, hugePts := existing, msgAt(itRoom, 0, 3), msgAt(itRoom, 0, 4)
	again.Text, again.CID = "retry", "cid-retry"
	twin.CID = "cid-twin"
	hugePts.Pts = math.MaxInt64 + 1
	batch := []domain.Message{msgAt(itRoom, 0, 2), again, msgAt(itRoom, 0, 3), twin, hugePts, msgAt(itRoom, 0, 0), msgAt(itRoom, 0, 5)}
	want := []store.Outcome{store.Inserted, store.Duplicate, store.Inserted, store.Duplicate, store.Rejected, store.Rejected, store.Inserted}
	res := s.Insert(t.Context(), batch)
	for i, r := range res {
		rejectedOK := r.Outcome == store.Rejected && errors.Is(r.Err, apperr.ErrInvalidArgument)
		if r.Outcome != want[i] || (r.Outcome != store.Rejected && r.Err != nil) || (r.Outcome == store.Rejected && !rejectedOK) {
			t.Fatalf("result[%d] = %v (%v), want %v; all %+v", i, r.Outcome, r.Err, want[i], res)
		}
	}
	ks := make([]store.MsgKey, 0, len(batch))
	for _, m := range batch[:4] {
		ks = append(ks, store.KeyOf(m))
	}
	got, err := s.Find(t.Context(), itRoom, ks)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	cids := make([]string, len(got))
	for i, m := range got {
		cids[i] = m.CID
	}
	if !slices.Equal(cids, []string{"cid-1", "cid-2", "cid-3"}) {
		t.Fatalf("stored = %+v, want the first write of seq 1, 2 and 3", got)
	}
	raw, err := db.Collection(messagesCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: keys.Msg(itRoom, 0, 2)}}).Raw()
	if err != nil {
		t.Fatalf("FindOne raw: %v", err)
	}
	if names := fieldNames(t, raw); !slices.Equal(names, []string{"_id", "t", "f", "p", "k", "x", "c", "ts"}) {
		t.Fatalf("stored fields = %v", names)
	}
}

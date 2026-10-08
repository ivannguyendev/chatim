package itest

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/app"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func TestRealInfraRecountRestoresADriftedCount(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	roomID := createRoom(t, dialCore(t, core.cfg))
	room := parseRoom(t, roomID)
	db := it.mongo.Database(core.cfg.MongoDB)
	st := mongostore.New(db, mongostore.Options{})
	before, err := st.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := db.Collection("rooms").UpdateOne(t.Context(), bson.D{{Key: "_id", Value: int64(room)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "member_count", Value: 9}}}}); err != nil {
		t.Fatalf("drift member_count: %v", err)
	}
	live := make(chan *nats.Msg, 16)
	sub, err := it.nc.ChanSubscribe(core.cfg.Stream.LiveRoot+"."+itTenant+".room."+roomID+".evt.member_count_changed", live)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	counts := fmt.Sprintf("room=%s stored=9 counted=2\n", roomID)
	var out bytes.Buffer
	if err := app.RunRecount(t.Context(), core.cfg, app.RecountOptions{Room: room, DryRun: true}, quiet, &out); err != nil || out.String() != counts {
		t.Fatalf("dry run = %v, output %q; want %q", err, out.String(), counts)
	}
	if r, err := st.Get(t.Context(), room); err != nil || r.MemberCount != 9 || r.MemberCountVer != before.MemberCountVer {
		t.Fatalf("after dry run room = %+v, %v; want the drift kept at ver %d", r, err, before.MemberCountVer)
	}

	out.Reset()
	if err := app.RunRecount(t.Context(), core.cfg, app.RecountOptions{Room: room}, quiet, &out); err != nil {
		t.Fatalf("recount: %v (output %q)", err, out.String())
	}
	ver := before.MemberCountVer + 1
	if want := counts + fmt.Sprintf("member_count_ver=%d\n", ver); out.String() != want {
		t.Fatalf("recount output = %q, want %q", out.String(), want)
	}
	if r, err := st.Get(t.Context(), room); err != nil || r.MemberCount != 2 || r.MemberCountVer != ver {
		t.Fatalf("after recount room = %+v, %v; want 2 members at ver %d", r, err, ver)
	}
	id := pbconv.MemberCountEventID(room, ver)
	ev := awaitLiveEvents(t, live, id)[id].GetMemberCountChanged()
	if ev.GetMemberCount() != 2 || ev.GetMemberCountVer() != ver {
		t.Fatalf("member_count_changed = %+v, want 2 at ver %d", ev, ver)
	}
}

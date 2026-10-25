package itest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/app"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

const (
	itMembersUserIndex    = "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1_pinned_at_1_archived_at_1_ver_1"
	itOldMembersUserIndex = "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1_pinned_at_1_archived_at_1"
	itMentionReadIndex    = "room_id_1_target_1_thread_root_1_seq_1"
)

func itIndexNames(t *testing.T, db *mongo.Database, coll string) []string {
	t.Helper()
	specs, err := db.Collection(coll).Indexes().ListSpecifications(t.Context())
	if err != nil {
		t.Fatalf("list indexes of %s: %v", coll, err)
	}
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	slices.Sort(names)
	return names
}

func seedOutdatedIndexes(t *testing.T, db *mongo.Database) {
	t.Helper()
	if err := mongostore.Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	members := db.Collection("members")
	if _, err := members.InsertOne(t.Context(), bson.D{{Key: "_id", Value: []byte("seed")}, {Key: "tenant", Value: itTenant}}); err != nil {
		t.Fatalf("seed a member doc: %v", err)
	}
	if err := members.Indexes().DropOne(t.Context(), itMembersUserIndex); err != nil {
		t.Fatalf("drop the new members index: %v", err)
	}
	old := bson.D{
		{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "room_id", Value: 1},
		{Key: "joined_at", Value: 1}, {Key: "cleared_at", Value: 1}, {Key: "read_seq", Value: 1},
		{Key: "pinned_at", Value: 1}, {Key: "archived_at", Value: 1},
	}
	if _, err := members.Indexes().CreateOne(t.Context(), mongo.IndexModel{Keys: old}); err != nil {
		t.Fatalf("create the old members index: %v", err)
	}
	clustered := options.CreateCollection().SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}})
	if err := db.CreateCollection(t.Context(), "mentions", clustered); err != nil {
		t.Fatalf("create mentions: %v", err)
	}
	read := mongo.IndexModel{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "target", Value: 1}, {Key: "thread_root", Value: 1}, {Key: "seq", Value: 1}}}
	if _, err := db.Collection("mentions").Indexes().CreateOne(t.Context(), read); err != nil {
		t.Fatalf("create the old mentions read index: %v", err)
	}
}

func runIndexes(t *testing.T, cfg config.Config, mode app.IndexesMode) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := app.RunIndexes(t.Context(), cfg, app.IndexesOptions{Mode: mode}, quiet, &out)
	return out.String(), err
}

func TestRealInfraStartupFlagsOutdatedIndexesAndTheCommandReplacesThem(t *testing.T) {
	it := realInfra(t)
	cfg := it.coreConfig(t, nil)
	db := it.mongo.Database(cfg.MongoDB)
	seedOutdatedIndexes(t, db)
	core := startCore(t, it, nil)
	members, mentions := itIndexNames(t, db, "members"), itIndexNames(t, db, "mentions")
	if slices.Contains(members, itMembersUserIndex) || !slices.Contains(members, itOldMembersUserIndex) || !slices.Contains(mentions, itMentionReadIndex) {
		t.Fatalf("after startup members %v, mentions %v; want the old indexes kept and no new members index built", members, mentions)
	}
	for coll, want := range map[string]float64{"members": 1, "mentions": 1, "rooms": 0} {
		if got := itMetric(t, core.cfg, `chatim_core_index_outdated{collection="`+coll+`"}`); got != want {
			t.Errorf("index_outdated{%s} = %v, want %v", coll, got, want)
		}
	}

	out, err := runIndexes(t, cfg, app.IndexesDryRun)
	for _, line := range []string{
		"members missing " + itMembersUserIndex,
		"members outdated " + itOldMembersUserIndex + " replaced by " + itMembersUserIndex + " (absent)",
		"mentions retired " + itMentionReadIndex,
	} {
		if err != nil || !strings.Contains(out, line+"\n") {
			t.Fatalf("dry run = %v, output %q; want the line %q", err, out, line)
		}
	}
	if got := itIndexNames(t, db, "members"); !slices.Equal(got, members) {
		t.Fatalf("members after dry run = %v, want unchanged %v", got, members)
	}

	if out, err := runIndexes(t, cfg, app.IndexesDropOld); !errors.Is(err, mongoindex.ErrReplacementNotReady) || out != "" {
		t.Fatalf("drop-old before apply = %v, output %q; want ErrReplacementNotReady", err, out)
	}
	if got := itIndexNames(t, db, "mentions"); !slices.Equal(got, mentions) {
		t.Fatalf("mentions after a refused drop-old = %v, want unchanged %v", got, mentions)
	}

	if out, err := runIndexes(t, cfg, app.IndexesApply); err != nil || out != "created members "+itMembersUserIndex+"\n" {
		t.Fatalf("apply = %v, output %q", err, out)
	}
	if got := itIndexNames(t, db, "members"); !slices.Contains(got, itMembersUserIndex) || !slices.Contains(got, itOldMembersUserIndex) {
		t.Fatalf("members after apply = %v, want both the new and the old index", got)
	}
	if out, err := runIndexes(t, cfg, app.IndexesApply); err != nil || out != "no missing index\n" {
		t.Fatalf("second apply = %v, output %q", err, out)
	}

	want := "dropped members " + itOldMembersUserIndex + "\ndropped mentions " + itMentionReadIndex + "\n"
	if out, err := runIndexes(t, cfg, app.IndexesDropOld); err != nil || out != want {
		t.Fatalf("drop-old = %v, output %q; want %q", err, out, want)
	}
	if got := itIndexNames(t, db, "members"); slices.Contains(got, itOldMembersUserIndex) || !slices.Contains(got, itMembersUserIndex) {
		t.Fatalf("members after drop-old = %v, want only the new user index", got)
	}
	if got := itIndexNames(t, db, "mentions"); slices.Contains(got, itMentionReadIndex) {
		t.Fatalf("mentions after drop-old = %v, want the read index gone", got)
	}
	if out, err := runIndexes(t, cfg, app.IndexesDropOld); err != nil || out != "no outdated index\n" {
		t.Fatalf("second drop-old = %v, output %q", err, out)
	}
	if out, err := runIndexes(t, cfg, app.IndexesDryRun); err != nil || out != "indexes up to date\n" {
		t.Fatalf("final dry run = %v, output %q", err, out)
	}
}

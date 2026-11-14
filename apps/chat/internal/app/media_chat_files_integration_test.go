package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func itMediaDatabase(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("CHATIM_IT_MONGO_URI")
	if uri == "" {
		t.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	db := client.Database("chatim_it_media_" + hex.EncodeToString(suffix))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", db.Name(), err)
		}
	})
	if err := mongostore.Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return db
}

func rawMessage(t *testing.T, db *mongo.Database, seq uint64) bson.M {
	t.Helper()
	var raw bson.M
	if err := db.Collection("messages").FindOne(t.Context(), bson.D{{Key: "_id", Value: keys.Msg(mediaRoom, 0, seq)}}).Decode(&raw); err != nil {
		t.Fatalf("raw doc %d: %v", seq, err)
	}
	return raw
}

func TestRealMongoMessageFilesFollowsALiveThenDeletedMessage(t *testing.T) {
	db := itMediaDatabase(t)
	st := mongostore.New(db, mongostore.Options{})
	files := mediaMessageFiles{messages: st}
	pdf := domain.Attachment{Key: mediaFileKey + "x", Name: "r.pdf", Mime: "application/pdf", Size: 3}
	msgs := []domain.Message{
		{Room: mediaRoom, Seq: 1, Tenant: mediaTenant, From: "alice", Kind: domain.KindText, Text: "x", CID: "c1", CreatedAt: mediaSent, Attachments: []domain.Attachment{photo("a.png"), pdf}},
		{Room: mediaRoom, Seq: 2, Tenant: mediaTenant, From: "alice", Kind: domain.KindText, Text: "y", CID: "c2", CreatedAt: mediaSent.Add(time.Second)},
	}
	for i, res := range st.Insert(t.Context(), msgs) {
		if res.Outcome != store.Inserted {
			t.Fatalf("insert %d: %+v", i, res)
		}
	}
	check := func(seq uint64, want mediaapp.MessageState) {
		t.Helper()
		got, err := files.MessageFiles(t.Context(), ref("77", 0, seq))
		if err != nil {
			t.Fatalf("MessageFiles(%d): %v", seq, err)
		}
		if got.Found != want.Found || got.Deleted != want.Deleted || got.Tenant != want.Tenant || !slices.Equal(got.Keys, want.Keys) {
			t.Fatalf("MessageFiles(%d) = %+v, want %+v", seq, got, want)
		}
	}
	check(1, mediaapp.MessageState{Found: true, Tenant: mediaTenant, Keys: []string{mediaFileKey, mediaFileKey + "x"}})
	check(2, mediaapp.MessageState{Found: true, Tenant: mediaTenant})
	check(9, mediaapp.MessageState{})
	if _, has := rawMessage(t, db, 1)["files"]; !has {
		t.Fatal("live doc has no files field")
	}
	if err := st.ApplyEdit(t.Context(), domain.Edit{Room: mediaRoom, Seq: 1, Version: 1, Kind: domain.EditDelete, Tenant: mediaTenant, By: "alice", At: mediaSent.Add(time.Minute)}); err != nil {
		t.Fatalf("ApplyEdit delete: %v", err)
	}
	raw := rawMessage(t, db, 1)
	if _, has := raw["files"]; has || raw["d"] != true {
		t.Fatalf("deleted doc still has files or no delete flag: %v", raw)
	}
	check(1, mediaapp.MessageState{Found: true, Deleted: true, Tenant: mediaTenant})
	if _, err := files.MessageFiles(t.Context(), ref("77x", 0, 1)); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("bad room err = %v", err)
	}
}

func TestRealMongoRoomAvatarsReadsTheStoredRoom(t *testing.T) {
	st := mongostore.New(itMediaDatabase(t), mongostore.Options{})
	seedAvatarRooms(t, st)
	assertRoomAvatars(t, mediaRoomAvatars{rooms: st}, setGroupAvatar(t, st))
}

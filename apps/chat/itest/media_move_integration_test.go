package itest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itMoveLimit = 30 * time.Second
	itMovePoll  = 100 * time.Millisecond
)

func uploadFile(t *testing.T, media chatimv1.MediaServiceClient, body []byte) string {
	t.Helper()
	ctx := callerAs(t.Context(), itUser)
	up, err := retryingUnavailable(ctx, func(ctx context.Context) (*chatimv1.StartUploadResponse, error) {
		return media.StartUpload(ctx, &chatimv1.StartUploadRequest{FileName: "move.png", ContentType: "image/png", Size: int64(len(body))})
	})
	if err != nil || len(up.GetUrls()) != 1 {
		t.Fatalf("StartUpload: %v %v", up, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.GetUrls()[0].GetUrl(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatalf("PUT part: %v", err)
	}
	_ = resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || etag == "" {
		t.Fatalf("PUT part: status %d etag %q", resp.StatusCode, etag)
	}
	if _, err := media.CompleteUpload(ctx, &chatimv1.CompleteUploadRequest{Key: up.GetKey(), Parts: []*chatimv1.UploadedPart{{PartNumber: 1, Etag: etag}}}); err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	return up.GetKey()
}

func awaitMove(t *testing.T, what string, check func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), itMoveLimit)
	defer cancel()
	for {
		err := check(ctx)
		if err == nil {
			return
		}
		if !backoff.Pause(ctx, itMovePoll) {
			t.Fatalf("%s not reached within %v: %v", what, itMoveLimit, err)
		}
	}
}

func uploadDoc(ctx context.Context, db *mongo.Database, key string) (bson.M, error) {
	var doc bson.M
	err := db.Collection("media_uploads").FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&doc)
	return doc, err
}

func pairState(db *mongo.Database, roomID string, seq uint64, key string, want int32) func(context.Context) error {
	id := fmt.Sprintf("%s-0-%d|%s", roomID, seq, key)
	return func(ctx context.Context) error {
		var pair struct {
			State int32 `bson:"state"`
		}
		if err := db.Collection("media_message_files").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&pair); err != nil {
			return err
		}
		if pair.State != want {
			return fmt.Errorf("pair %s state %d, want %d", id, pair.State, want)
		}
		return nil
	}
}

func objectIn(m itMedia, bucket, key string, want bool) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := m.s3.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
		switch found := err == nil; {
		case found == want:
			return nil
		case found || minio.ToErrorResponse(err).Code == "NoSuchKey":
			return fmt.Errorf("object %s in %s: found %v, want %v", key, bucket, found, want)
		default:
			return err
		}
	}
}

func consumerIdle(it *itInfra, stream string) func(context.Context) error {
	return func(ctx context.Context) error {
		c, err := it.js.Consumer(ctx, stream, "media")
		if err != nil {
			return err
		}
		info, err := c.Info(ctx)
		if err != nil {
			return err
		}
		if info.NumPending != 0 || info.NumAckPending != 0 {
			return fmt.Errorf("media consumer has %d pending and %d unacked", info.NumPending, info.NumAckPending)
		}
		return nil
	}
}

func TestRealInfraSentFilesMoveToCompletedAndRetireWithTheirLastMessage(t *testing.T) {
	it := realInfra(t)
	m := mediaBuckets(t, it)
	m.env["MEDIA_READY_CHECK_INTERVAL"] = "200ms"
	chat := startChatNode(t, it, m.env)
	client, media := dialChat(t, chat.cfg), dialMedia(t, chat.cfg.GRPCAddr)
	db := it.mongo.Database(chat.cfg.MongoDB)
	body := []byte("move me to completed")
	key := uploadFile(t, media, body)
	first, second := createRoomWith(t, client, []string{itUser, "bob"}), createRoomWith(t, client, []string{itUser})
	seq := sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{
		RoomId: first, Cid: "move-1", Attachments: []*chatimv1.Attachment{{Key: key, Name: "move.png", Mime: "image/png", Size: int64(len(body))}},
	})

	awaitMove(t, "file in completed", objectIn(m, m.completed, key, true))
	awaitMove(t, "file gone from processing", objectIn(m, m.processing, key, false))
	awaitMove(t, "pair of the sent message", pairState(db, first, seq, key, 1))
	doc, err := uploadDoc(t.Context(), db, key)
	if err != nil || doc["bucket"] != "completed" || doc["attached_at"] == nil || doc["retired_at"] != nil {
		t.Fatalf("upload doc %v, %v", doc, err)
	}
	t.Logf("moved %s: bucket %v attached_at %v", key, doc["bucket"], doc["attached_at"])

	fwd := sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: second, Cid: "move-fwd", ForwardFrom: &chatimv1.ForwardRef{RoomId: first, Seq: seq}})
	awaitMove(t, "pair of the forward", pairState(db, second, fwd, key, 1))
	if err := deleteAs(t.Context(), client, itUser, first, seq); err != nil {
		t.Fatalf("delete original: %v", err)
	}
	awaitMove(t, "original pair released", pairState(db, first, seq, key, 2))
	awaitMove(t, "media consumer idle", consumerIdle(it, chat.cfg.Stream.Name))
	if doc, err := uploadDoc(t.Context(), db, key); err != nil || doc["retired_at"] != nil {
		t.Fatalf("file retired while the forward holds it: %v %v", doc, err)
	}
	if err := objectIn(m, m.completed, key, true)(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := deleteAs(t.Context(), client, itUser, second, fwd); err != nil {
		t.Fatalf("delete forward: %v", err)
	}
	awaitMove(t, "forward pair released", pairState(db, second, fwd, key, 2))
	awaitMove(t, "file retired", func(ctx context.Context) error {
		doc, err := uploadDoc(ctx, db, key)
		if err != nil {
			return err
		}
		if doc["retired_at"] == nil {
			return errors.New("retired_at not set")
		}
		return nil
	})
	t.Logf("retired %s after its last message was deleted", key)
}

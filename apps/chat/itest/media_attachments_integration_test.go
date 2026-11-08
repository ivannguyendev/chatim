package itest

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itS3Region = "us-east-1"

var itKeyCode = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)

type itMedia struct {
	env                   map[string]string
	s3                    *minio.Client
	processing, completed string
}

func mediaEnv(t *testing.T, it *itInfra) map[string]string {
	t.Helper()
	return mediaBuckets(t, it).env
}

func mediaBuckets(t *testing.T, it *itInfra) itMedia {
	t.Helper()
	endpoint := os.Getenv("CHATIM_IT_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set CHATIM_IT_S3_ENDPOINT to run")
	}
	access, secret := os.Getenv("CHATIM_IT_S3_ACCESS_KEY"), os.Getenv("CHATIM_IT_S3_SECRET_KEY")
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := minio.DefaultTransport(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	s3, err := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Region: itS3Region, BucketLookup: minio.BucketLookupPath, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	processing, completed := "chatim-it-chat-p-"+it.suffix, "chatim-it-chat-c-"+it.suffix
	for _, b := range []string{processing, completed} {
		if err := s3.MakeBucket(t.Context(), b, minio.MakeBucketOptions{Region: itS3Region}); err != nil {
			t.Fatalf("make bucket %s: %v", b, err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), itCleanupLimit)
			defer cancel()
			for obj := range s3.ListObjects(ctx, b, minio.ListObjectsOptions{Recursive: true}) {
				_ = s3.RemoveObject(ctx, b, obj.Key, minio.RemoveObjectOptions{})
			}
			_ = s3.RemoveBucket(ctx, b)
		})
	}
	return itMedia{
		env: map[string]string{
			"MEDIA_S3_ENDPOINT": endpoint, "MEDIA_S3_PUBLIC_ENDPOINT": endpoint, "MEDIA_S3_REGION": itS3Region,
			"MEDIA_S3_ACCESS_KEY": access, "MEDIA_S3_SECRET_KEY": secret,
			"MEDIA_S3_BUCKET_PROCESSING": processing, "MEDIA_S3_BUCKET_COMPLETED": completed,
		},
		s3: s3, processing: processing, completed: completed,
	}
}

func dialMedia(t *testing.T, addr string) chatimv1.MediaServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return chatimv1.NewMediaServiceClient(conn)
}

func attachmentKey(user string) string {
	var id [16]byte
	var millis [8]byte
	now := time.Now()
	binary.BigEndian.PutUint64(millis[:], uint64(now.UnixMilli()))
	copy(id[:6], millis[2:])
	_, _ = rand.Read(id[6:])
	return itTenant + "/" + user + "/" + now.UTC().Format("20060102") + "/" + itKeyCode.EncodeToString(id[:])
}

func sendFile(t *testing.T, client chatimv1.ChatServiceClient, roomID, cid, name string) (uint64, string) {
	t.Helper()
	key := attachmentKey(itUser)
	seq := sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{
		RoomId: roomID, Cid: cid, Attachments: []*chatimv1.Attachment{{Key: key, Name: name, Mime: "image/png", Size: 9}},
	})
	return seq, key
}

func TestRealInfraAttachmentURLsFollowTheChatReadCheck(t *testing.T) {
	it := realInfra(t)
	env := mediaEnv(t, it)
	env["MEDIA_READY_CHECK_INTERVAL"] = "200ms"
	chat := startChatNode(t, it, env)
	client, media := dialChat(t, chat.cfg), dialMedia(t, chat.cfg.GRPCAddr)
	shared := createRoomWith(t, client, []string{itUser, "bob"})
	private := createRoomWith(t, client, []string{itUser})
	shown, shownKey := sendFile(t, client, shared, "media-shown", "shown.png")
	plain := sendAs(t, client, itUser, shared, "media-plain", "no file")
	hidden, _ := sendFile(t, client, shared, "media-hidden", "hidden.png")
	gone, _ := sendFile(t, client, shared, "media-gone", "gone.png")
	secret, secretKey := sendFile(t, client, private, "media-private", "private.png")
	if _, err := client.HideMessage(callerAs(t.Context(), "bob"), &chatimv1.HideMessageRequest{RoomId: shared, Seq: hidden}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if err := deleteAs(t.Context(), client, itUser, shared, gone); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	ask := func(user string, refs ...*chatimv1.MessageRef) *chatimv1.GetAttachmentURLsResponse {
		t.Helper()
		resp, err := retryingUnavailable(callerAs(t.Context(), user), func(ctx context.Context) (*chatimv1.GetAttachmentURLsResponse, error) {
			return media.GetAttachmentURLs(ctx, &chatimv1.GetAttachmentURLsRequest{Messages: refs})
		})
		if err != nil {
			t.Fatalf("GetAttachmentURLs as %s: %v", user, err)
		}
		return resp
	}
	ref := func(roomID string, seq uint64) *chatimv1.MessageRef {
		return &chatimv1.MessageRef{RoomId: roomID, Seq: seq}
	}
	bob := ask("bob", ref(shared, gone), ref(private, secret), ref(shared, hidden), ref(shared, plain), ref(shared, shown), ref(shared, shown))
	if len(bob.GetMessages()) != 1 || bob.GetMessages()[0].GetSeq() != shown || bob.GetMessages()[0].GetRoomId() != shared {
		t.Fatalf("bob got %v, want only seq %d", bob.GetMessages(), shown)
	}
	file := bob.GetMessages()[0].GetFiles()[0]
	if file.GetKey() != shownKey || !strings.Contains(file.GetUrl(), "X-Amz-Signature=") || strings.Contains(file.GetUrl(), "response-content-") || file.GetThumbnailUrl() != "" || !bob.GetExpiresAt().IsValid() {
		t.Fatalf("bob's file %v", file)
	}
	alice := ask(itUser, ref(private, secret), ref(shared, hidden), ref(shared, shown))
	got := alice.GetMessages()
	if len(got) != 3 || got[0].GetFiles()[0].GetKey() != secretKey || got[1].GetSeq() != hidden || got[2].GetSeq() != shown {
		t.Fatalf("alice got %v, want private, hidden-for-bob and shown in request order", got)
	}
}

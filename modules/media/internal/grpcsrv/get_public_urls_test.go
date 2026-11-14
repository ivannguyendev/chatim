package grpcsrv_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestGetPublicURLsOverTheService(t *testing.T) {
	s, uploads := newService(t)
	avatar := storetest.Uploaded("acme", "an", "x", "image/png")
	avatar.Key = domain.NewObjectKey("acme", "an", true, time.Now(), [10]byte{1})
	if err := uploads.Insert(t.Context(), avatar); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPublicURLs(caller("acme", "binh"), &chatimv1.GetPublicURLsRequest{Keys: []string{avatar.Key}})
	if err != nil || len(got.GetFiles()) != 1 || !got.GetExpiresAt().IsValid() {
		t.Fatalf("GetPublicURLs = %v, %v", got, err)
	}
	if f := got.GetFiles()[0]; f.GetKey() != avatar.Key || f.GetUrl() != "https://s3/"+avatar.Key || f.GetThumbnailUrl() != "https://s3/"+avatar.Key+".thumb" {
		t.Fatalf("file %v", f)
	}
	if _, err := s.GetPublicURLs(context.Background(), &chatimv1.GetPublicURLsRequest{Keys: []string{avatar.Key}}); err == nil {
		t.Fatal("no caller accepted")
	}
}

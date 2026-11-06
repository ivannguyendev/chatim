package grpcsrv_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/chat/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func asCaller() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(chatrpc.TenantHeader, "acme", chatrpc.UserHeader, "an"))
}

func TestGetClientConfigReturnsWhatTheAppBuilt(t *testing.T) {
	want := &chatimv1.GetClientConfigResponse{
		Message: &chatimv1.MessageLimits{MaxTextBytes: 16384, MaxAttachments: 10},
		Media:   &chatimv1.MediaFeature{Enabled: true, MaxParallelUploads: 4},
	}
	d := depsOf(t, memStores(), options{sender: &fakeSender{}})
	d.ClientConfig = func() *chatimv1.GetClientConfigResponse { return want }
	svc, err := grpcsrv.New(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetClientConfig(asCaller(), &chatimv1.GetClientConfigRequest{})
	if err != nil || !proto.Equal(got, want) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := svc.GetClientConfig(context.Background(), &chatimv1.GetClientConfigRequest{}); err == nil {
		t.Fatal("no caller accepted")
	}
}

func TestGetClientConfigWithoutWiringIsUnavailable(t *testing.T) {
	svc, err := grpcsrv.New(depsOf(t, memStores(), options{sender: &fakeSender{}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetClientConfig(asCaller(), &chatimv1.GetClientConfigRequest{}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

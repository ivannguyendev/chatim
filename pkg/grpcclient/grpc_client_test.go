package grpcclient

import (
	"context"
	"testing"

	"go.uber.org/goleak"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"default service config", Options{Creds: insecure.NewCredentials()}, false},
		{"invalid service config", Options{Creds: insecure.NewCredentials(), ServiceConfig: `{"methodConfig": 1}`}, true},
		{"missing creds", Options{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := New("dns:///localhost:50051", tt.opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() err = %v, wantErr %v", err, tt.wantErr)
			}
			if conn != nil {
				conn.Close()
			}
		})
	}
}

func TestWrapDownstreamUnary(t *testing.T) {
	invoker := func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
		return status.Error(codes.InvalidArgument, "sku must not be empty")
	}
	err := wrapDownstreamUnary(t.Context(), "/inventory.v1.Inventory/Reserve", nil, nil, nil, invoker)

	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("status.Code = %v, want InvalidArgument", got)
	}
	if _, direct := err.(interface{ GRPCStatus() *status.Status }); direct {
		t.Error("error is still a direct status; the server boundary would forward it")
	}
}

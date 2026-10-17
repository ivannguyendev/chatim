package grpcsrv_test

import (
	"log/slog"
	"testing"

	"go.uber.org/goleak"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var quiet = slog.New(slog.DiscardHandler)

func expectCode(t *testing.T, what string, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(grpcserver.ToStatus(err)); got != want {
		t.Fatalf("%s: code %v (%v), want %v", what, got, err, want)
	}
}

package grpcclient

import (
	"context"
	"net"
	"slices"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const resolverServiceConfig = `{
  "methodConfig": [{
    "name": [{"service": "grpc.health.v1.Health"}],
    "retryPolicy": {
      "maxAttempts": 5,
      "initialBackoff": "0.01s",
      "maxBackoff": "0.01s",
      "backoffMultiplier": 1,
      "retryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`

func TestDefaultRetryPolicyAndThrottlingStillApply(t *testing.T) {
	h, dial := serveUnavailableHealth(t)
	conn := dialHealth(t, "passthrough:///bufnet", dial)
	if got := h.attemptsPerCall(t, conn, 3); !slices.Equal(got, []int32{3, 2, 1}) {
		t.Fatalf("attempts per call = %v, want 3 from the retry policy, then fewer as retryThrottling spends its tokens", got)
	}
}

func TestServiceConfigFromTheResolverIsIgnored(t *testing.T) {
	h, dial := serveUnavailableHealth(t)
	r := manual.NewBuilderWithScheme("chatimtest")
	r.BuildCallback = func(_ resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) {
		r.InitialState(resolver.State{
			Addresses:     []resolver.Address{{Addr: "bufnet"}},
			ServiceConfig: cc.ParseServiceConfig(resolverServiceConfig),
		})
	}
	conn := dialHealth(t, "chatimtest:///core", dial, grpc.WithResolvers(r))
	if got := h.attemptsPerCall(t, conn, 1); !slices.Equal(got, []int32{3}) {
		t.Fatalf("attempts = %v, want the 3 of the default policy, not the 5 the resolver published", got)
	}
}

type unavailableHealth struct {
	healthpb.UnimplementedHealthServer
	attempts atomic.Int32
}

func (h *unavailableHealth) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	h.attempts.Add(1)
	return nil, status.Error(codes.Unavailable, "warming up")
}

func (h *unavailableHealth) attemptsPerCall(t *testing.T, conn *grpc.ClientConn, calls int) []int32 {
	t.Helper()
	client := healthpb.NewHealthClient(conn)
	var out []int32
	for range calls {
		before := h.attempts.Load()
		if _, err := client.Check(t.Context(), &healthpb.HealthCheckRequest{}); status.Code(err) != codes.Unavailable {
			t.Fatalf("Check = %v, want Unavailable", err)
		}
		out = append(out, h.attempts.Load()-before)
	}
	return out
}

func serveUnavailableHealth(t *testing.T) (*unavailableHealth, grpc.DialOption) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	h := &unavailableHealth{}
	healthpb.RegisterHealthServer(srv, h)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		<-done
	})
	return h, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
}

func dialHealth(t *testing.T, target string, opts ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()
	conn, err := New(target, Options{Creds: insecure.NewCredentials(), DialOptions: opts})
	if err != nil {
		t.Fatalf("New(%s): %v", target, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

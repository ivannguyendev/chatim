package grpcsrv_test

import (
	"context"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/grpcserver"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/resilience"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var (
	quiet       = slog.New(slog.DiscardHandler)
	actorConfig = actor.Config{Mailbox: 16, Idle: time.Minute, MaxGroup: 8, MaxActors: 64, GroupDeadline: 2 * time.Second, ReservationTTL: 10 * time.Second}
	flushConfig = flush.Config{Shards: 2, Window: time.Millisecond, MaxBatch: 64, QueueSize: 64, InsertTimeout: 500 * time.Millisecond}
	sentinels   = map[codes.Code]string{
		codes.NotFound:           apperr.ErrNotFound.Error(),
		codes.InvalidArgument:    apperr.ErrInvalidArgument.Error(),
		codes.FailedPrecondition: apperr.ErrFailedPrecondition.Error(),
		codes.PermissionDenied:   apperr.ErrPermissionDenied.Error(),
		codes.Unauthenticated:    apperr.ErrUnauthenticated.Error(),
		codes.Unavailable:        apperr.ErrUnavailable.Error(),
		codes.ResourceExhausted:  apperr.ErrResourceExhausted.Error(),
		codes.Internal:           "internal error",
	}
)

type rig struct {
	client    chatimv1.CoreServiceClient
	rooms     *memstore.Rooms
	msgs      *memstore.Messages
	edits     *memstore.Edits
	hidden    *memstore.Hidden
	reactions *memstore.Interactions
	pins      *memstore.Pins
}

type options struct {
	sender  grpcsrv.Sender
	newID   func() uint64
	now     func() time.Time
	limiter *resilience.Limiter
	policy  access.Policy
	events  grpcsrv.EventPublisher
	limits  mutate.Limits
}

func newRig(t *testing.T, o options) *rig {
	t.Helper()
	rg := memStores()
	if o.sender == nil {
		o.sender = startRouter(t, rg)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{
		Sender: o.sender, Rooms: rg.rooms, Pages: rg.msgs, NewID: o.newID, Now: o.now, Policy: o.policy, Events: o.events,
		Mutator: newMutator(t, rg, o), Edits: rg.edits, Hidden: rg.hidden,
	}, quiet)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rg.client = serve(t, svc, o.limiter)
	return rg
}

func startRouter(t *testing.T, rg *rig) *actor.Router {
	t.Helper()
	fl, err := flush.New(rg.msgs, flushConfig)
	if err != nil {
		t.Fatalf("flush.New: %v", err)
	}
	router, err := actor.NewRouter(rg.msgs, rg.rooms, fl, acceptAllCIDs{}, nopPublisher{}, actorConfig, quiet)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { _ = fl.Run(ctx) })
	wg.Go(func() { _ = router.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	for !router.Started() {
		runtime.Gosched()
	}
	return router
}

func serve(t *testing.T, svc chatimv1.CoreServiceServer, limiter *resilience.Limiter) chatimv1.CoreServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpcserver.New(grpcserver.Config{Limiter: limiter}, quiet)
	chatimv1.RegisterCoreServiceServer(srv, svc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ServeListener(ctx, lis) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		cancel()
		<-done
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeListener: %v", err)
		}
	})
	return chatimv1.NewCoreServiceClient(conn)
}

func as(t *testing.T, tenant, user string) context.Context {
	return metadata.AppendToOutgoingContext(t.Context(), grpcsrv.TenantHeader, tenant, grpcsrv.UserHeader, user)
}

func expectCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	st := status.Convert(err)
	if st.Code() != code || st.Message() != sentinels[code] {
		t.Fatalf("status = (%v, %q), want (%v, %q)", st.Code(), st.Message(), code, sentinels[code])
	}
}

func (rg *rig) createGroup(t *testing.T, tenant, creator string, members ...string) string {
	t.Helper()
	resp, err := rg.client.CreateRoom(as(t, tenant, creator), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: append([]string{creator}, members...),
	})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	return resp.GetRoom().GetId()
}

func (rg *rig) send(t *testing.T, ctx context.Context, room, cid, text string) *chatimv1.SendMessageResponse {
	t.Helper()
	resp, err := rg.client.SendMessage(ctx, &chatimv1.SendMessageRequest{RoomId: room, Cid: cid, Text: text})
	if err != nil {
		t.Fatalf("SendMessage(%s): %v", cid, err)
	}
	return resp
}

func idSequence(seq ...uint64) (next func() uint64, calls func() int) {
	var mu sync.Mutex
	n := 0
	next = func() uint64 {
		mu.Lock()
		defer mu.Unlock()
		n++
		return seq[min(n, len(seq))-1]
	}
	calls = func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
	return next, calls
}

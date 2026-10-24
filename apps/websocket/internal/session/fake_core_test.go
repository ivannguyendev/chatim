package session

import (
	"context"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/command"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type fakeCore struct {
	mu      sync.Mutex
	holds   map[uint64]chan codes.Code
	panics  map[uint64]bool
	nils    map[uint64]bool
	started chan uint64
	claims  chan jwtverify.Claims
	calls   atomic.Int64
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		holds:   map[uint64]chan codes.Code{},
		panics:  map[uint64]bool{},
		nils:    map[uint64]bool{},
		started: make(chan uint64, 256),
		claims:  make(chan jwtverify.Claims, 256),
	}
}

func (f *fakeCore) Execute(ctx context.Context, claims jwtverify.Claims, req *chatimv1.ClientRequest) (*chatimv1.ServerResponse, command.Outcome) {
	f.calls.Add(1)
	f.started <- req.GetId()
	f.claims <- claims
	resp := &chatimv1.ServerResponse{Id: req.GetId()}
	if req.GetRequest() == nil {
		resp.Code = int32(codes.Unimplemented)
		return resp, command.Outcome{Code: codes.Unimplemented}
	}
	f.mu.Lock()
	hold, panics, nothing := f.holds[req.GetId()], f.panics[req.GetId()], f.nils[req.GetId()]
	f.mu.Unlock()
	if panics {
		panic("command exploded")
	}
	if nothing {
		return nil, command.Outcome{Command: "Test", Code: codes.OK}
	}
	code := codes.OK
	if hold != nil {
		select {
		case code = <-hold:
		case <-ctx.Done():
			code = codes.Canceled
		}
	}
	if code != codes.OK {
		resp.Code = int32(code)
		resp.Message = code.String()
	}
	return resp, command.Outcome{Command: "Test", Code: code}
}

func (f *fakeCore) hold(id uint64) chan codes.Code {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan codes.Code, 1)
	f.holds[id] = ch
	return ch
}

func (f *fakeCore) panicOn(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panics[id] = true
}

func (f *fakeCore) startedIDs() []uint64 {
	var ids []uint64
	for {
		select {
		case id := <-f.started:
			ids = append(ids, id)
		default:
			return ids
		}
	}
}

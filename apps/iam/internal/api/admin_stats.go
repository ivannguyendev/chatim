package api

import (
	"sync"
	"sync/atomic"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
)

type AdminStats struct {
	OK               atomic.Uint64
	Denied           atomic.Uint64
	Unauthenticated  atomic.Uint64
	BadRequest       atomic.Uint64
	NotFound         atomic.Uint64
	Conflict         atomic.Uint64
	Limited          atomic.Uint64
	Unavailable      atomic.Uint64
	Errors           atomic.Uint64
	UserLimited      atomic.Uint64
	InflightRejected atomic.Uint64

	deniedOnce sync.Once
	deniedBy   map[string]*atomic.Uint64
}

func (s *AdminStats) denials() map[string]*atomic.Uint64 {
	s.deniedOnce.Do(func() {
		s.deniedBy = make(map[string]*atomic.Uint64, len(authz.Required))
		for rpc := range authz.Required {
			s.deniedBy[rpc] = &atomic.Uint64{}
		}
	})
	return s.deniedBy
}

func (s *AdminStats) DeniedByRPC() map[string]uint64 {
	by := s.denials()
	out := make(map[string]uint64, len(by))
	for rpc, n := range by {
		out[rpc] = n.Load()
	}
	return out
}

func (s *AdminStats) count(rpc string, err error) {
	if err == nil {
		s.OK.Add(1)
		return
	}
	switch connect.CodeOf(err) {
	case connect.CodePermissionDenied:
		s.Denied.Add(1)
		if n, ok := s.denials()[rpc]; ok {
			n.Add(1)
		}
	case connect.CodeUnauthenticated:
		s.Unauthenticated.Add(1)
	case connect.CodeInvalidArgument:
		s.BadRequest.Add(1)
	case connect.CodeNotFound:
		s.NotFound.Add(1)
	case connect.CodeAlreadyExists, connect.CodeFailedPrecondition:
		s.Conflict.Add(1)
	case connect.CodeResourceExhausted:
		s.Limited.Add(1)
	case connect.CodeUnavailable:
		s.Unavailable.Add(1)
	default:
		s.Errors.Add(1)
	}
}

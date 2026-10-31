package api

import (
	"sync/atomic"

	"connectrpc.com/connect"
)

type DirectoryStats struct {
	GetUsersOK              atomic.Uint64
	GetUsersBadRequest      atomic.Uint64
	GetUsersUnauthenticated atomic.Uint64
	GetUsersLimited         atomic.Uint64
	GetUsersUnavailable     atomic.Uint64
	GetUsersErrors          atomic.Uint64
	GetUsersIDs             atomic.Uint64
	UserLimited             atomic.Uint64
	InflightRejected        atomic.Uint64
}

func (s *DirectoryStats) countGetUsers(err error) {
	if err == nil {
		s.GetUsersOK.Add(1)
		return
	}
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument:
		s.GetUsersBadRequest.Add(1)
	case connect.CodeUnauthenticated:
		s.GetUsersUnauthenticated.Add(1)
	case connect.CodeResourceExhausted:
		s.GetUsersLimited.Add(1)
	case connect.CodeUnavailable:
		s.GetUsersUnavailable.Add(1)
	default:
		s.GetUsersErrors.Add(1)
	}
}

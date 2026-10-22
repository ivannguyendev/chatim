package interest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Router interface {
	Any() (coreroute.Conn, error)
	Trigger()
}

type CoreLoader struct {
	router   Router
	deadline time.Duration
}

func NewCoreLoader(router Router, deadline time.Duration) (*CoreLoader, error) {
	if router == nil || deadline <= 0 {
		return nil, errors.New("interest: core loader needs a router and a positive deadline")
	}
	return &CoreLoader{router: router, deadline: deadline}, nil
}

func (l *CoreLoader) Load(ctx context.Context, tenant, user string) (Membership, error) {
	conn, err := l.router.Any()
	if err != nil {
		return Membership{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, l.deadline)
	defer cancel()
	callCtx = coreroute.WithCaller(metadata.NewOutgoingContext(callCtx, metadata.MD{}), tenant, user)
	resp, err := conn.Core.ListMyRoomIDs(callCtx, &chatimv1.ListMyRoomIDsRequest{})
	if err != nil {
		if status.Code(err) == codes.Unavailable {
			l.router.Trigger()
		}
		return Membership{}, err
	}
	joined, err := roomsOf(resp.GetRooms())
	if err != nil {
		return Membership{}, err
	}
	left, err := roomsOf(resp.GetLeft())
	if err != nil {
		return Membership{}, err
	}
	return Membership{Joined: joined, Left: left}, nil
}

func roomsOf(list []*chatimv1.MyRoomID) ([]Room, error) {
	rooms := make([]Room, 0, len(list))
	for _, r := range list {
		id, err := ids.ParseRoomID(r.GetRoomId())
		if err != nil {
			return nil, fmt.Errorf("list my room ids: %w", err)
		}
		rooms = append(rooms, Room{ID: id, Ver: r.GetVer()})
	}
	return rooms, nil
}

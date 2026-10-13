package roomlist

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MaxRooms  = 100
	TailFills = 16
)

var (
	errMissingDeps = fmt.Errorf("%w: room list needs a room list store, a room tail store, a hidden store, four caches and a positive join limit", apperr.ErrInvalidArgument)
	errLimit       = fmt.Errorf("%w: limit", apperr.ErrInvalidArgument)
	errFolder      = fmt.Errorf("%w: folder", apperr.ErrInvalidArgument)
	errRoomIDs     = fmt.Errorf("%w: room ids", apperr.ErrInvalidArgument)
)

type HiddenRanges interface {
	HiddenInRooms(ctx context.Context, user string, ranges []store.HiddenRange) (map[uint64][]uint64, error)
}

type Deps struct {
	List      store.RoomList
	Tails     store.RoomTails
	Hidden    HiddenRanges
	Info      *roomcache.InfoCache
	Tail      *roomcache.TailCache
	Snapshots *roomcache.SnapshotCache
	Paths     *roomcache.PathCache
	JoinMax   int
}

type Lister struct {
	list    store.RoomList
	tails   store.RoomTails
	hidden  HiddenRanges
	info    *roomcache.InfoCache
	tail    *roomcache.TailCache
	snaps   *roomcache.SnapshotCache
	paths   *roomcache.PathCache
	joinMax int
}

func New(d Deps) (*Lister, error) {
	if d.List == nil || d.Tails == nil || d.Hidden == nil || d.Info == nil || d.Tail == nil || d.Snapshots == nil || d.Paths == nil || d.JoinMax < 1 {
		return nil, errMissingDeps
	}
	return &Lister{
		list: d.List, tails: d.Tails, hidden: d.Hidden, info: d.Info, tail: d.Tail, snaps: d.Snapshots, paths: d.Paths, joinMax: d.JoinMax,
	}, nil
}

func storeErr(op string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", op, err)
	default:
		return fmt.Errorf("%s: %w: %s", op, apperr.ErrUnavailable, err.Error())
	}
}

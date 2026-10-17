package store

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

const RoomActivityBatch = 1000

type RoomListQuery struct {
	Tenant string
	User   string
	Folder domain.RoomFolder
}

type MemberKey struct {
	Room uint64
	User string
}

type RoomActivity struct {
	Room          uint64
	LastMessageAt time.Time
	LastSeq       uint64
	LastEditAt    time.Time
}

type RoomOrderRow struct {
	Member   domain.Member
	Activity RoomActivity
}

type RoomList interface {
	CountRooms(ctx context.Context, tenant, user string, limit int) (int, error)
	JoinedRoomOrder(ctx context.Context, q RoomListQuery) ([]RoomOrderRow, error)
	StepwiseRoomOrder(ctx context.Context, q RoomListQuery) ([]RoomOrderRow, error)
	MembersByKeys(ctx context.Context, keys []MemberKey) ([]domain.Member, error)
	RoomActivities(ctx context.Context, rooms []uint64) ([]RoomActivity, error)
	RoomInfos(ctx context.Context, rooms []uint64) ([]domain.Room, error)
}

func (r RoomOrderRow) SortKey() domain.RoomSortKey {
	m := r.Member
	return domain.NewRoomSortKey(m.Room, m.Preferences.PinnedAt, r.Activity.LastMessageAt, m.JoinedAt)
}

func SortRoomOrder(rows []RoomOrderRow) {
	slices.SortFunc(rows, func(a, b RoomOrderRow) int { return domain.CompareRoomSortKeys(a.SortKey(), b.SortKey()) })
}

func (q RoomListQuery) Validate() error {
	if q.Folder != domain.FolderMain && q.Folder != domain.FolderArchived {
		return invalid("folder")
	}
	return domain.ValidUser(q.User)
}

func (q RoomListQuery) Holds(p domain.RoomPreferences) bool { return domain.FolderOf(p) == q.Folder }

func ValidateCountRooms(user string, limit int) error {
	if limit < 1 {
		return invalid("limit")
	}
	return domain.ValidUser(user)
}

func ValidateRoomIDs(rooms []uint64) error {
	if len(rooms) > RoomActivityBatch {
		return invalid("room ids")
	}
	for _, r := range rooms {
		if r == 0 || r > math.MaxInt64 {
			return invalid("room id")
		}
	}
	return nil
}

func ValidateMemberKeys(keys []MemberKey) error {
	if len(keys) > MaxMemberScan {
		return invalid("member keys")
	}
	for _, k := range keys {
		if k.Room == 0 || k.Room > math.MaxInt64 {
			return invalid("member room")
		}
		if err := domain.ValidUser(k.User); err != nil {
			return err
		}
	}
	return nil
}

package mentionapp

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type (
	Action       = domain.Action
	MessageKey   = domain.MessageKey
	Member       = domain.Member
	MemberCursor = domain.MemberCursor
	MemberRoom   = domain.MemberRoom
)

const (
	ReadMembers = domain.ActionReadMembers
	ReadHistory = domain.ActionReadHistory
)

type CoreReader interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]Member, error)
	ListMembers(ctx context.Context, room uint64, after MemberCursor, limit int) ([]Member, error)
	RoomMemberIDs(ctx context.Context, room uint64) ([]string, error)
	RoomMemberCount(ctx context.Context, room uint64) (int64, error)
	RoomsOf(ctx context.Context, tenant, user string) ([]MemberRoom, error)
	SentTimes(ctx context.Context, keys []MessageKey) (map[MessageKey]time.Time, error)
	VisibleMessages(ctx context.Context, tenant, user string, keys []MessageKey) ([]*chatimv1.Message, error)
	MessageVersions(ctx context.Context, keys []MessageKey) (map[MessageKey]uint32, error)
	Authorize(ctx context.Context, tenant, user string, room uint64, action Action) error
}

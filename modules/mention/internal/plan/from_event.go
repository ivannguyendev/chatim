package plan

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var ErrInvalidEvent = fmt.Errorf("%w: mention event", apperr.ErrInvalidArgument)

type Kind uint8

const (
	Created Kind = 1
	Edited  Kind = 2
	Deleted Kind = 3
)

type Result struct {
	Plan     domain.Plan
	Current  []domain.Target
	Previous []domain.Target
	RoomType domain.RoomType
	Sender   string
	Ts       time.Time
	Kind     Kind
}

func FromEvent(ev *chatimv1.Event) (Result, error) {
	msg, previous, kind := payloadOf(ev)
	if msg == nil {
		return Result{}, invalid("payload")
	}
	room, err := strconv.ParseUint(ev.GetRoomId(), 10, 64)
	if err != nil {
		return Result{}, invalid("room id")
	}
	if !valid(ev.GetTs()) {
		return Result{}, invalid("ts")
	}
	at := ev.GetTs().AsTime()
	sent := at
	if valid(msg.GetCreatedAt()) {
		sent = msg.GetCreatedAt().AsTime()
	}
	if msg.GetDeleted() {
		kind = Deleted
	}
	var current []domain.Target
	if kind != Deleted {
		if current, err = targetsOf(msg.GetMentionTargets(), msg.GetMentionAll(), msg.GetMentionAllAt(), sent); err != nil {
			return Result{}, err
		}
	}
	before, err := targetsOf(previous.GetTargets(), previous.GetAll(), previous.GetAllAt(), sent)
	if err != nil {
		return Result{}, err
	}
	p := domain.Plan{
		Key:    domain.MessageKey{Room: room, Thread: ev.GetThreadRoot(), Seq: ev.GetSeq()},
		Tenant: ev.GetTenant(), Sender: msg.GetSender(), Ver: msg.GetVer(),
		Live: slices.Clone(current), Retired: retired(before, current),
	}
	if err := p.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidEvent, err)
	}
	return Result{
		Plan: p, Current: current, Previous: before, RoomType: roomTypeOf(ev.GetRoomType()),
		Sender: p.Sender, Ts: at, Kind: kind,
	}, nil
}

func payloadOf(ev *chatimv1.Event) (*chatimv1.Message, *chatimv1.MentionSet, Kind) {
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return p.MessageCreated.GetMessage(), nil, Created
	case *chatimv1.Event_MessageEdited:
		return p.MessageEdited.GetMessage(), p.MessageEdited.GetPreviousMentions(), Edited
	case *chatimv1.Event_MessageDeleted:
		return p.MessageDeleted.GetMessage(), p.MessageDeleted.GetPreviousMentions(), Deleted
	default:
		return nil, nil, 0
	}
}

func roomTypeOf(t chatimv1.RoomType) domain.RoomType {
	switch t {
	case chatimv1.RoomType_ROOM_TYPE_DM:
		return domain.RoomDM
	case chatimv1.RoomType_ROOM_TYPE_GROUP:
		return domain.RoomGroup
	default:
		return domain.RoomChannel
	}
}

func valid(t *timestamppb.Timestamp) bool { return t.CheckValid() == nil }

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalidEvent, field) }

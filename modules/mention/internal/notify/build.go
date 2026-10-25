package notify

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

const PacketKind = "mention"

type Kind uint8

const (
	KindNotified  Kind = 1
	KindRetracted Kind = 2
)

type Notice struct {
	Kind    Kind
	Subject string
	Packet  *chatimv1.WsPublish
}

type Builder struct {
	Root string
	Page int
}

func (b Builder) Notified(r plan.Result, msg *chatimv1.Message, add map[string]domain.Target) ([]Notice, error) {
	groups := groupByTarget(add)
	var out []Notice
	for _, g := range groups {
		n, err := envelope(r, 'm', targetKey(g.target))
		if err != nil {
			return nil, err
		}
		n.Payload = &chatimv1.Notification_MentionNotified{MentionNotified: &chatimv1.MentionNotified{Message: msg, Target: targetOf(g.target)}}
		packets, err := b.paged(KindNotified, n, g.users)
		if err != nil {
			return nil, err
		}
		out = append(out, packets...)
	}
	return out, nil
}

func (b Builder) Retracted(r plan.Result, drop map[string]domain.Target) ([]Notice, error) {
	if len(drop) == 0 {
		return nil, nil
	}
	reason, ok := retractReasons[r.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: retract a mention of a created message", plan.ErrInvalidEvent)
	}
	n, err := envelope(r, 'r', reason.key)
	if err != nil {
		return nil, err
	}
	n.Payload = &chatimv1.Notification_MentionRetracted{MentionRetracted: &chatimv1.MentionRetracted{Reason: reason.proto}}
	return b.paged(KindRetracted, n, slices.Collect(maps.Keys(drop)))
}

func (b Builder) paged(kind Kind, n *chatimv1.Notification, users []string) ([]Notice, error) {
	subject, err := wspublish.Subject(b.Root, n.GetTenant())
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if err := ids.ValidUser(u); err != nil {
			return nil, err
		}
	}
	out := make([]Notice, 0, len(users)/max(b.Page, 1)+1)
	var part uint32
	for _, page := range wspublish.Pages(users, b.Page) {
		part++
		out = append(out, Notice{Kind: kind, Subject: subject, Packet: &chatimv1.WsPublish{
			Id: wspublish.PageID(n.GetId(), int(part), page), Tenant: n.GetTenant(), Kind: PacketKind, Notification: n,
			Targets: &chatimv1.Targets{Users: page}, Part: part,
		}})
	}
	for _, o := range out {
		o.Packet.Parts = part
	}
	return out, nil
}

func ID(key domain.MessageKey, mark byte, ver uint32, target string) string {
	return strconv.FormatUint(key.Room, 10) + "-" + strconv.FormatUint(key.Thread, 10) + "-" +
		strconv.FormatUint(key.Seq, 10) + "-" + string(mark) + strconv.FormatUint(uint64(ver), 10) + "-" + target
}

type group struct {
	target domain.Target
	users  []string
}

func groupByTarget(add map[string]domain.Target) []group {
	byKey := make(map[string]*group)
	for user, t := range add {
		k := targetKey(t)
		g, ok := byKey[k]
		if !ok {
			g = &group{target: t}
			byKey[k] = g
		}
		if t.At.Before(g.target.At) {
			g.target = t
		}
		g.users = append(g.users, user)
	}
	out := make([]group, 0, len(byKey))
	for _, g := range byKey {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b group) int { return cmp.Compare(targetKey(a.target), targetKey(b.target)) })
	return out
}

func targetKey(t domain.Target) string {
	switch t.Kind {
	case domain.TargetUser:
		return "u:" + t.ID
	case domain.TargetGroup:
		return "g:" + t.ID
	default:
		return "all"
	}
}

type retractReason struct {
	proto chatimv1.RetractReason
	key   string
}

var retractReasons = map[plan.Kind]retractReason{
	plan.Edited:  {chatimv1.RetractReason_RETRACT_REASON_EDITED, "edited"},
	plan.Deleted: {chatimv1.RetractReason_RETRACT_REASON_DELETED, "deleted"},
}

var roomTypes = map[domain.RoomType]chatimv1.RoomType{
	domain.RoomDM:    chatimv1.RoomType_ROOM_TYPE_DM,
	domain.RoomGroup: chatimv1.RoomType_ROOM_TYPE_GROUP,
}

var targetKinds = map[domain.TargetKind]chatimv1.MentionKind{
	domain.TargetUser:  chatimv1.MentionKind_MENTION_KIND_USER,
	domain.TargetGroup: chatimv1.MentionKind_MENTION_KIND_GROUP,
	domain.TargetAll:   chatimv1.MentionKind_MENTION_KIND_ALL,
}

func envelope(r plan.Result, mark byte, target string) (*chatimv1.Notification, error) {
	if err := ids.ValidTenant(r.Plan.Tenant); err != nil {
		return nil, err
	}
	key := r.Plan.Key
	return &chatimv1.Notification{
		Id:         ID(key, mark, r.Plan.Ver, target),
		Tenant:     r.Plan.Tenant,
		RoomId:     strconv.FormatUint(key.Room, 10),
		RoomType:   roomTypes[r.RoomType],
		ThreadRoot: key.Thread,
		Seq:        key.Seq,
		MessageVer: r.Plan.Ver,
		Ts:         timestamppb.New(r.Ts),
	}, nil
}

func targetOf(t domain.Target) *chatimv1.MentionTarget {
	out := &chatimv1.MentionTarget{Kind: targetKinds[t.Kind], Id: t.ID}
	if !t.At.IsZero() {
		out.At = timestamppb.New(t.At)
	}
	return out
}

package publish

import (
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMalformed = errors.New("event needs an id, a subject-safe tenant and a known payload")

func Message(root string, room uint64, ev *chatimv1.Event) (*nats.Msg, error) {
	kind, ok := eventKind(ev)
	if !ok || ev.GetId() == "" || !validToken(ev.GetTenant()) {
		return nil, errMalformed
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: roomSubject(root, ev.GetTenant(), room, kind), Data: data, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, ev.GetId())
	return m, nil
}

func eventKind(ev *chatimv1.Event) (string, bool) {
	switch ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return msgCreated, true
	case *chatimv1.Event_RoomCreated:
		return roomCreated, true
	case *chatimv1.Event_MessageEdited:
		return msgEdited, true
	case *chatimv1.Event_MessageDeleted:
		return msgDeleted, true
	default:
		return "", false
	}
}

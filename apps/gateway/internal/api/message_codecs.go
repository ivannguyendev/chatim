package api

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var errNotEnvelope = errors.New("api: codec got a message that is not an envelope")

type envelope struct {
	msg proto.Message
}

func requestInitializer(mt protoreflect.MessageType) connect.HandlerOption {
	return connect.WithRequestInitializer(func(_ connect.Spec, message any) error {
		e, ok := message.(*envelope)
		if !ok {
			return errNotEnvelope
		}
		e.msg = mt.New().Interface()
		return nil
	})
}

func messageOf(v any) (proto.Message, error) {
	e, ok := v.(*envelope)
	if !ok || e.msg == nil {
		return nil, fmt.Errorf("%w: %T", errNotEnvelope, v)
	}
	return e.msg, nil
}

type protoCodec struct{}

func (protoCodec) Name() string { return "proto" }

func (protoCodec) Marshal(v any) ([]byte, error) {
	m, err := messageOf(v)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(m)
}

func (protoCodec) Unmarshal(data []byte, v any) error {
	m, err := messageOf(v)
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, m)
}

type jsonCodec struct{}

func (jsonCodec) Name() string { return "json" }

func (jsonCodec) Marshal(v any) ([]byte, error) {
	m, err := messageOf(v)
	if err != nil {
		return nil, err
	}
	return protojson.Marshal(m)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
	m, err := messageOf(v)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, m)
}

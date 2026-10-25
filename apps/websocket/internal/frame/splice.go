package frame

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	eventField        = serverFrameField("event")
	notificationField = serverFrameField("notification")
)

func serverFrameField(name protoreflect.Name) protowire.Number {
	field := (&chatimv1.ServerFrame{}).ProtoReflect().Descriptor().Fields().ByName(name)
	if field == nil || field.Kind() != protoreflect.MessageKind {
		panic("frame: ServerFrame has no message field " + string(name))
	}
	return field.Number()
}

func Event(raw []byte) []byte {
	return splice(eventField, raw)
}

func Notification(raw []byte) []byte {
	return splice(notificationField, raw)
}

func splice(field protowire.Number, raw []byte) []byte {
	size := protowire.SizeTag(field) + protowire.SizeBytes(len(raw))
	out := make([]byte, 0, size)
	out = protowire.AppendTag(out, field, protowire.BytesType)
	return protowire.AppendBytes(out, raw)
}

package command

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/ivannguyendev/chatim/pkg/corecall"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	requestOneof = (&chatimv1.ClientRequest{}).ProtoReflect().Descriptor().Oneofs().ByName("request")
	resultOneof  = (&chatimv1.ServerResponse{}).ProtoReflect().Descriptor().Oneofs().ByName("result")
)

type command struct {
	method string
	result protoreflect.FieldDescriptor
}

func servedMethods() []string { return corecall.All() }

func checks() map[string]corecall.Check {
	return map[string]corecall.Check{chatimv1.CoreService_SendMessage_FullMethodName: checkSendMessage}
}

func buildCommands(served []corecall.Command) (map[protoreflect.FieldNumber]command, []string, error) {
	byRequest := make(map[protoreflect.FullName]corecall.Command, len(served))
	for _, c := range served {
		byRequest[c.Request.FullName()] = c
	}
	fields := requestOneof.Fields()
	cmds := make(map[protoreflect.FieldNumber]command, fields.Len())
	methods := make([]string, 0, fields.Len())
	for i := range fields.Len() {
		field := fields.Get(i)
		c, ok := byRequest[field.Message().FullName()]
		if !ok {
			return nil, nil, fmt.Errorf("command: %s has no core call", field.Message().FullName())
		}
		delete(byRequest, field.Message().FullName())
		result := resultOneof.Fields().ByNumber(field.Number())
		if result == nil || result.Message() == nil || result.Message().FullName() != c.Response.FullName() {
			return nil, nil, fmt.Errorf("command: result field %d does not carry %s", field.Number(), c.Response.FullName())
		}
		cmds[field.Number()] = command{method: c.Method, result: result}
		methods = append(methods, c.Method)
	}
	if len(byRequest) > 0 {
		return nil, nil, fmt.Errorf("command: %d core calls have no client request field", len(byRequest))
	}
	return cmds, methods, nil
}

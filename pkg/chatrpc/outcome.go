package chatrpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const fallbackMessage = "request failed"

var (
	chatMessageKept = map[codes.Code]bool{
		codes.InvalidArgument: true, codes.NotFound: true, codes.FailedPrecondition: true,
		codes.PermissionDenied: true, codes.AlreadyExists: true, codes.ResourceExhausted: true,
	}
	fixedMessages = map[codes.Code]string{
		codes.Canceled:         "request canceled",
		codes.DeadlineExceeded: "chat did not answer in time",
		codes.Unavailable:      "chat unavailable, retry later",
		codes.Unimplemented:    "request not supported",
	}
)

type Outcome struct {
	Command string
	Code    codes.Code
	Detail  string
	Message string
}

type grpcStatusError interface {
	GRPCStatus() *status.Status
}

func codeOf(err error) (codes.Code, string) {
	var withStatus grpcStatusError
	if errors.As(err, &withStatus) {
		if st := withStatus.GRPCStatus(); st != nil && st.Code() != codes.OK && st.Code() <= codes.Unauthenticated {
			return st.Code(), st.Message()
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return codes.DeadlineExceeded, ""
	case errors.Is(err, context.Canceled):
		return codes.Canceled, ""
	}
	return codes.Unknown, ""
}

func ClientMessage(code codes.Code, chatMessage string) string {
	if chatMessageKept[code] && chatMessage != "" {
		return chatMessage
	}
	if m, ok := fixedMessages[code]; ok {
		return m
	}
	return fallbackMessage
}

package session

import (
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type frameErrorReason int

const (
	frameErrorUnsupported frameErrorReason = iota
	frameErrorInvalid
	frameErrorRate
	frameErrorReasonCount
)

var (
	frameErrorLabels = [frameErrorReasonCount]string{"unsupported", "invalid", "rate"}
	frameErrorWire   = [frameErrorReasonCount]chatimv1.FrameErrorReason{
		chatimv1.FrameErrorReason_FRAME_ERROR_REASON_UNSUPPORTED,
		chatimv1.FrameErrorReason_FRAME_ERROR_REASON_INVALID,
		chatimv1.FrameErrorReason_FRAME_ERROR_REASON_RATE,
	}
)

func (c *connection) frameError(reason frameErrorReason) {
	c.server.stats.frameErrors[reason].Add(1)
	if !c.rejects.Allow() {
		return
	}
	c.sendFrame(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_FrameError{
		FrameError: &chatimv1.FrameErrorFrame{Reason: frameErrorWire[reason]},
	}}, sendControl)
}

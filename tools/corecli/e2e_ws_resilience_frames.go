package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/lxzan/gws"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wstest"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const codeTooManyConnections uint16 = 4009

var garbageFrame = []byte{0xff, 0xff, 0xff, 0xff}

func (r *wsResilienceRun) frameErrors() error {
	if err := r.refused(gws.OpcodeText, []byte("hello"), chatimv1.FrameErrorReason_FRAME_ERROR_REASON_UNSUPPORTED); err != nil {
		return err
	}
	if !backoff.Pause(r.base, frameErrorSpacing) {
		return r.base.Err()
	}
	if err := r.refused(gws.OpcodeBinary, garbageFrame, chatimv1.FrameErrorReason_FRAME_ERROR_REASON_INVALID); err != nil {
		return err
	}
	if err := r.u.barrier(); err != nil {
		return fmt.Errorf("a request after both frame errors: %w", err)
	}
	return nil
}

func (r *wsResilienceRun) refused(opcode gws.Opcode, payload []byte, want chatimv1.FrameErrorReason) error {
	mark := r.u.feed.Len()
	if err := r.u.ws.SendRaw(opcode, payload); err != nil {
		return fmt.Errorf("%s sends a frame for %v: %w", r.u.name, want, err)
	}
	if _, err := r.u.await(mark, e2e.FrameErrorMatch(want)); err != nil {
		return err
	}
	return stillOpen(r.u)
}

func stillOpen(p *wsPeer) error {
	select {
	case <-p.ws.Done():
		return fmt.Errorf("%s: the connection ended with %w, want it kept open", p.name, p.ws.Err())
	default:
		return nil
	}
}

func (r *wsResilienceRun) userCapped() error {
	user := "e2e-wsrv-" + r.room
	for i := range r.userCap {
		v := r.peer(fmt.Sprintf("V%d", i+1), user)
		r.vs = append(r.vs, v)
		if err := r.dial(v); err != nil {
			return err
		}
	}
	what := fmt.Sprintf("V's connection %d", r.userCap+1)
	code, closed, err := r.overCap(user)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := e2e.CheckClosedWith(what, code, closed, codeTooManyConnections); err != nil {
		return err
	}
	for _, v := range r.vs {
		if err := stillOpen(v); err != nil {
			return err
		}
	}
	r.notes = append(r.notes, fmt.Sprintf("%d connections of V ready, the next closed with %d", r.userCap, code))
	return nil
}

func (r *wsResilienceRun) overCap(user string) (uint16, bool, error) {
	token, _, err := r.sign(user, devTokenTTL)
	if err != nil {
		return 0, false, err
	}
	ws, err := dialWS(r.base, r.url, token, r.timeout)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = ws.Close() }()
	ctx, cancel := context.WithTimeout(r.base, r.wait)
	defer cancel()
	select {
	case <-ws.Done():
	case <-ctx.Done():
		return 0, false, fmt.Errorf("still open after %v, want close %d", r.wait, codeTooManyConnections)
	}
	closeErr, ok := errors.AsType[*wstest.CloseError](ws.Err())
	if !ok {
		return 0, false, nil
	}
	return closeErr.Code, true, nil
}

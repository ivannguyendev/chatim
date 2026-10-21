package wsclient

import (
	"errors"
	"io"
	"testing"
)

func TestBrokenConnectionIsClosedAndKeepsTheCause(t *testing.T) {
	err := closeErrorOf(io.ErrUnexpectedEOF)
	if !errors.Is(err, ErrClosed) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("closeErrorOf(EOF) = %v, want ErrClosed wrapping the cause", err)
	}
	if _, ok := errors.AsType[*CloseError](err); ok {
		t.Fatalf("closeErrorOf(EOF) = %v, want no close code", err)
	}
}

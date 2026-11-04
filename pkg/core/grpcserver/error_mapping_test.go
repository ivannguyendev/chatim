package grpcserver_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

func TestToStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"nil", nil, codes.OK},
		{"wrapped not found", fmt.Errorf("get order: %w", apperr.ErrNotFound), codes.NotFound},
		{"wrapped invalid", fmt.Errorf("parse id: %w", apperr.ErrInvalidArgument), codes.InvalidArgument},
		{"dependency outage", fmt.Errorf("call inventory: %w", apperr.ErrUnavailable), codes.Unavailable},
		{"wrapped resource exhausted", fmt.Errorf("room 42 mailbox: %w", apperr.ErrResourceExhausted), codes.ResourceExhausted},
		{"context canceled", fmt.Errorf("query: %w", context.Canceled), codes.Canceled},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"status kept", status.Error(codes.Aborted, "retry"), codes.Aborted},
		{"downstream invalid is our bug", fmt.Errorf("call inventory: %w", status.Error(codes.InvalidArgument, "bad sku")), codes.Internal},
		{"downstream unavailable", fmt.Errorf("call inventory: %w", status.Error(codes.Unavailable, "down")), codes.Unavailable},
		{"downstream deadline", fmt.Errorf("call inventory: %w", status.Error(codes.DeadlineExceeded, "slow")), codes.DeadlineExceeded},
		{"unknown", errors.New("boom"), codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(grpcserver.ToStatus(tt.err)); got != tt.want {
				t.Errorf("ToStatus(%v) code = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestToStatusUsesSentinelMessage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"not found", fmt.Errorf("room 42 tenant acme: %w", apperr.ErrNotFound), "not found"},
		{"resource exhausted", fmt.Errorf("room 42 mailbox 1024: %w", apperr.ErrResourceExhausted), "resource exhausted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Convert(grpcserver.ToStatus(tt.err)).Message(); got != tt.want {
				t.Errorf("ToStatus(%v) message = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

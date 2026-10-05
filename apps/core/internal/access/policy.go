package access

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Action string

const (
	ReadHistory     Action = "read_history"
	SendMessage     Action = "send_message"
	EditMessage     Action = "edit_message"
	DeleteMessage   Action = "delete_message"
	HideMessage     Action = "hide_message"
	ClearHistory    Action = "clear_history"
	ReadEditHistory Action = "read_edit_history"
)

var ErrDenied = fmt.Errorf("action denied: %w", apperr.ErrPermissionDenied)

type Request struct {
	Action Action
	User   string
	Author string
	Room   domain.Room
	Member domain.Member
}

type Policy interface {
	Check(ctx context.Context, req Request) error
}

type PolicyFunc func(ctx context.Context, req Request) error

func (f PolicyFunc) Check(ctx context.Context, req Request) error { return f(ctx, req) }

type AllowMembers struct{}

func (AllowMembers) Check(context.Context, Request) error { return nil }

type DefaultPolicy struct{}

func (DefaultPolicy) Check(_ context.Context, req Request) error {
	if (req.Action == EditMessage || req.Action == DeleteMessage) && req.Author != req.User {
		return ErrDenied
	}
	return nil
}

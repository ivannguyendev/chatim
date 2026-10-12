package domain

import (
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	ErrRoomNotFound    = fmt.Errorf("room %w", apperr.ErrNotFound)
	ErrNotMember       = fmt.Errorf("not a member: %w", apperr.ErrPermissionDenied)
	ErrBusy            = fmt.Errorf("room busy: %w", apperr.ErrResourceExhausted)
	ErrRetryLater      = fmt.Errorf("retry later: %w", apperr.ErrUnavailable)
	ErrMessageNotFound = fmt.Errorf("message %w", apperr.ErrNotFound)
	ErrMessageDeleted  = fmt.Errorf("message deleted: %w", apperr.ErrFailedPrecondition)
	ErrVersionConflict = fmt.Errorf("message version conflict: %w", apperr.ErrFailedPrecondition)
	ErrTooManyPins     = fmt.Errorf("too many pinned messages: %w", apperr.ErrFailedPrecondition)
	ErrEmojiNotAllowed = fmt.Errorf("emoji not allowed: %w", apperr.ErrInvalidArgument)
	ErrDirectRoom      = fmt.Errorf("direct room members are fixed: %w", apperr.ErrFailedPrecondition)
	ErrLastOwner       = fmt.Errorf("last owner cannot step down: %w", apperr.ErrFailedPrecondition)
	ErrMemberNotFound  = fmt.Errorf("member %w", apperr.ErrNotFound)
	ErrTooManyMembers  = fmt.Errorf("too many members: %w", apperr.ErrInvalidArgument)
	ErrHasReplies      = fmt.Errorf("message has replies: %w", apperr.ErrFailedPrecondition)
	ErrSelfDirect      = fmt.Errorf("direct room with yourself: %w", apperr.ErrInvalidArgument)
)

func CheckTenant(room Room, tenant string) error {
	if room.Tenant != tenant {
		return ErrRoomNotFound
	}
	return nil
}

func invalid(field string) error {
	return fmt.Errorf("%w: %s", apperr.ErrInvalidArgument, field)
}

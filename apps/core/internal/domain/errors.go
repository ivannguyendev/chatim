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
	ErrTooManyEmojis   = fmt.Errorf("too many reaction emojis: %w", apperr.ErrFailedPrecondition)
	ErrTooManyPins     = fmt.Errorf("too many pinned messages: %w", apperr.ErrFailedPrecondition)
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

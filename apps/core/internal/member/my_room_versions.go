package member

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func (r *Reader) MyRoomVersions(ctx context.Context, tenant, user string) ([]store.RoomVersion, error) {
	if err := domain.ValidTenant(tenant); err != nil {
		return nil, err
	}
	if err := domain.ValidUser(user); err != nil {
		return nil, err
	}
	rooms, err := r.d.Members.RoomVersionsOf(ctx, tenant, user)
	if err != nil {
		return nil, readFailure("room versions of the caller", err)
	}
	return rooms, nil
}

func readFailure(op string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", op, err)
	}
	return fmt.Errorf("%s: %w: %s", op, apperr.ErrUnavailable, err.Error())
}

package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestDomainErrorsWrapAppKinds(t *testing.T) {
	tests := []struct {
		err  error
		kind error
	}{
		{domain.ErrRoomNotFound, apperr.ErrNotFound},
		{domain.ErrNotMember, apperr.ErrPermissionDenied},
		{domain.ErrBusy, apperr.ErrResourceExhausted},
		{domain.ErrRetryLater, apperr.ErrUnavailable},
		{domain.ErrMessageNotFound, apperr.ErrNotFound},
		{domain.ErrMessageDeleted, apperr.ErrFailedPrecondition},
		{domain.ErrVersionConflict, apperr.ErrFailedPrecondition},
		{domain.ErrEmojiNotAllowed, apperr.ErrInvalidArgument},
		{domain.ErrTooManyPins, apperr.ErrFailedPrecondition},
		{domain.ErrDirectRoom, apperr.ErrFailedPrecondition},
		{domain.ErrLastOwner, apperr.ErrFailedPrecondition},
		{domain.ErrMemberNotFound, apperr.ErrNotFound},
		{domain.ErrTooManyMembers, apperr.ErrInvalidArgument},
		{domain.ErrHasReplies, apperr.ErrFailedPrecondition},
		{domain.ErrSelfDirect, apperr.ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			if !errors.Is(tt.err, tt.kind) {
				t.Fatalf("%v does not wrap %v", tt.err, tt.kind)
			}
			if !errors.Is(fmt.Errorf("room 42: %w", tt.err), tt.err) {
				t.Fatalf("wrapped %v no longer matches itself", tt.err)
			}
		})
	}
}

func TestCheckTenant(t *testing.T) {
	room := domain.Room{ID: 42, Tenant: "acme"}
	if err := domain.CheckTenant(room, "acme"); err != nil {
		t.Fatalf("CheckTenant(same tenant) = %v, want nil", err)
	}
	for _, tenant := range []string{"other", "", "ACME"} {
		err := domain.CheckTenant(room, tenant)
		if !errors.Is(err, domain.ErrRoomNotFound) {
			t.Fatalf("CheckTenant(%q) = %v, want ErrRoomNotFound", tenant, err)
		}
		if err.Error() != domain.ErrRoomNotFound.Error() {
			t.Errorf("CheckTenant(%q) = %q, must be indistinguishable from a missing room %q", tenant, err, domain.ErrRoomNotFound)
		}
	}
}

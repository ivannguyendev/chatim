package mongostore

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func TestWriteProfileWithoutSeenValuesCreatesNothing(t *testing.T) {
	s := &Store{}
	w := store.ProfileWrite{TenantID: "shop", UserID: "lan", DisplayName: "Lan", At: time.Unix(1700000000, 0).UTC(), By: "jit:shop"}
	written, err := s.WriteProfile(t.Context(), w)
	if written || !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("WriteProfile without seen values = %v, %v; want false and invalid argument", written, err)
	}
}

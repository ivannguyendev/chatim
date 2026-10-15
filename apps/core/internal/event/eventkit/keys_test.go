package eventkit_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMessageKeyKeepsRoomThreadAndSeq(t *testing.T) {
	if got, want := eventkit.MessageKey(7, 3, 9), (store.MsgKey{Room: 7, Thread: 3, Seq: 9}); got != want {
		t.Fatalf("MessageKey = %+v, want %+v", got, want)
	}
}

func TestGoneAndUndeliverableClassifyMissingTargets(t *testing.T) {
	cases := []struct {
		err           error
		gone, undeliv bool
	}{
		{fmt.Errorf("wrap: %w", domain.ErrRoomNotFound), true, true},
		{apperr.ErrInvalidArgument, true, true},
		{store.ErrEditNotFound, true, false},
		{domain.ErrMessageNotFound, true, false},
		{fmt.Errorf("wrap: %w", domain.ErrMemberNotFound), true, false},
		{errors.New("store down"), false, false},
		{nil, false, false},
	}
	for _, c := range cases {
		if got := eventkit.Gone(c.err); got != c.gone {
			t.Errorf("Gone(%v) = %v, want %v", c.err, got, c.gone)
		}
		if got := eventkit.Undeliverable(c.err); got != c.undeliv {
			t.Errorf("Undeliverable(%v) = %v, want %v", c.err, got, c.undeliv)
		}
	}
}

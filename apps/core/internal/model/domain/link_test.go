package domain_test

import (
	"errors"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestValidateReplyAcceptsOnlyAMainTimelineSeq(t *testing.T) {
	for _, ok := range []*domain.ReplyRef{nil, {Seq: 1}, {Seq: 40}} {
		if err := domain.ValidateReply(ok); err != nil {
			t.Fatalf("ValidateReply(%+v) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []*domain.ReplyRef{{}, {Thread: 3, Seq: 40}, {Seq: math.MaxUint64}} {
		if err := domain.ValidateReply(bad); !errors.Is(err, apperr.ErrInvalidArgument) || err.Error() != "invalid argument: reply_to" {
			t.Fatalf("ValidateReply(%+v) = %v, want invalid reply_to", bad, err)
		}
	}
}

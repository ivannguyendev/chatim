package sweep

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	foreignMsg       = "pending state holds bits its collection does not know; they stay set"
	foreignKeysLimit = 4096
)

func (s *Sweeper) warnForeign(ctx context.Context, d store.PendingDoc) {
	extra := d.State &^ d.Collection.Bits()
	if extra == 0 {
		return
	}
	key := fmt.Sprintf("%d/%d/%d/%d/%s/%d/%d", d.Collection, d.Room, d.Thread, d.Seq, d.User, d.Kind, d.Ver)
	if _, seen := s.foreign[key]; seen {
		return
	}
	if len(s.foreign) >= foreignKeysLimit {
		clear(s.foreign)
	}
	s.foreign[key] = struct{}{}
	s.odd.Warn(ctx, foreignMsg, "collection", d.Collection.String(), "room", d.Room, "bits", uint32(extra))
}

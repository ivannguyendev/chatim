package consumer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/notify"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

type Mentions interface {
	Apply(ctx context.Context, plan domain.Plan) error
}

type Rooms interface {
	Raise(ctx context.Context, tenant string, room uint64, at time.Time) error
}

type Versions interface {
	MessageVersions(ctx context.Context, keys []domain.MessageKey) (map[domain.MessageKey]uint32, error)
}

type Resolver interface {
	Resolve(ctx context.Context, tenant string, room uint64, roomType domain.RoomType, sender string, targets []domain.Target) (map[string]domain.Target, error)
}

type Deps struct {
	Source   jsconsume.Source
	Mentions Mentions
	Rooms    Rooms
	Versions Versions
	Resolver Resolver
	Notify   notify.JetStream
	Log      *slog.Logger
}

func (d Deps) validate() error {
	if d.Source == nil || d.Mentions == nil || d.Rooms == nil || d.Versions == nil || d.Resolver == nil || d.Notify == nil {
		return fmt.Errorf("%w: mention consumer needs a source, mention and room stores, versions, a resolver and a notify publisher", apperr.ErrInvalidArgument)
	}
	return nil
}

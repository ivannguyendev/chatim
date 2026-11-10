package exchange

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var (
	errLinkPending = errors.New("user link not visible yet")
	errLinkUnknown = errors.New("user link returned no result")
)

var linkOutcomeOf = map[store.LinkResult]LinkOutcome{
	store.LinkCreated:  LinkCreated,
	store.LinkExisting: LinkExisting,
	store.LinkHealed:   LinkHealed,
}

func (s *Service) linkUser(ctx context.Context, sub trust.Subject, now time.Time) (string, error) {
	candidate, err := newUserID(s.deps.Random)
	if err != nil {
		return "", fmt.Errorf("%w: %w", apperr.ErrUnavailable, err)
	}
	linked, err := s.deps.Profiles.LinkUser(ctx, newUser(sub, candidate, now))
	switch {
	case err != nil && ctx.Err() != nil:
		return "", fmt.Errorf("%w: link user: %w", apperr.ErrUnavailable, ctx.Err())
	case linked.Result == store.LinkPending:
		s.deps.Stats.countLink(LinkPending)
		if err == nil {
			err = errLinkPending
		}
		return "", fmt.Errorf("%w: link user: %w", apperr.ErrUnavailable, err)
	case err != nil:
		return "", s.linkFailed(ctx, sub, err)
	}
	outcome, ok := linkOutcomeOf[linked.Result]
	if !ok {
		return "", s.linkFailed(ctx, sub, errLinkUnknown)
	}
	s.deps.Stats.countLink(outcome)
	return linked.ID, nil
}

func (s *Service) linkFailed(ctx context.Context, sub trust.Subject, err error) error {
	s.deps.Stats.storeErrors.Add(1)
	s.deps.Stats.countLink(LinkError)
	if errors.Is(err, store.ErrLinkCorrupt) {
		s.deps.Log.WarnContext(ctx, "user link corrupt", slog.String("tenant", sub.Tenant), slog.String("realm", sub.Realm))
	}
	return fmt.Errorf("%w: link user: %w", apperr.ErrUnavailable, err)
}

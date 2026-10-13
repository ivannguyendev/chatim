package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.RoomList = (*Store)(nil)

func (s *Store) CountRooms(ctx context.Context, tenant, user string, limit int) (int, error) {
	if err := store.ValidateCountRooms(user, limit); err != nil {
		return 0, err
	}
	n, err := s.members.CountDocuments(ctx, memberRoomsFilter(tenant, user), options.Count().SetLimit(int64(limit)))
	if err != nil {
		return 0, fmt.Errorf("count rooms of %q: %w", user, err)
	}
	return int(n), nil
}

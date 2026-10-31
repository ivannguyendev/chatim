package pgstore

import "context"

func (s *Store) MigrateUpTo(ctx context.Context, version int) ([]string, error) {
	return s.migrate(ctx, true, version)
}

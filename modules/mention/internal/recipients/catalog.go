package recipients

import (
	"context"

	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
)

type Catalog interface {
	Taggable(ctx context.Context, tenant string) (map[string]bool, error)
}

type StoreCatalog struct {
	Groups store.Groups
}

func (c StoreCatalog) Taggable(ctx context.Context, tenant string) (map[string]bool, error) {
	all, err := c.Groups.All(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(all))
	for _, g := range all {
		if g.Active {
			out[g.ID] = true
		}
	}
	return out, nil
}

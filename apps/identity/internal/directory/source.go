package directory

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

type Source interface {
	RevocationSource
	LoadPage(ctx context.Context, after store.UserKey, limit int) ([]store.DirectoryEntry, error)
	TryLoadSlot(ctx context.Context, purpose string, slots int) (store.Slot, bool, error)
	StreamStart(ctx context.Context) (store.StreamToken, error)
	OpenStream(ctx context.Context, from store.StreamToken) (store.ChangeStream, error)
}

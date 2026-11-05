package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func holdBeforeCommit(t *testing.T, hook func()) {
	t.Helper()
	beforeCommit = hook
	t.Cleanup(func() { beforeCommit = nil })
}

func onBatch(t *testing.T, hook func(n int)) {
	t.Helper()
	afterBatch = hook
	t.Cleanup(func() { afterBatch = nil })
}

func streamCursorID(cs store.ChangeStream) int64 {
	return cs.(*changeStream).cursor.ID()
}

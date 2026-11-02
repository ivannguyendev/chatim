package mongostore

import "testing"

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

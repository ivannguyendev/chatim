package mongostore

import "testing"

func holdBeforeCommit(t *testing.T, hook func()) {
	t.Helper()
	beforeCommit = hook
	t.Cleanup(func() { beforeCommit = nil })
}

package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

func requireSilent(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s ran before the schema was complete", what)
	default:
	}
}

func TestIncompleteSchemaKeepsTheReplicaFromWritingUntilMigrated(t *testing.T) {
	kek := newKEK(t)
	cfg := testConfig(kek)
	cfg.KeyReload = 20 * time.Millisecond
	st := &fakeStore{
		keys: []store.SigningKey{activeKey(t, kek)}, due: "k-next", dueAsked: make(chan time.Duration, 1), cleaned: make(chan struct{}, 1),
		outage: outage{reloads: make(chan struct{}, 1), slotAsked: make(chan struct{}, 1)},
	}
	st.missing.Store(true)
	r := runIdentity(t, cfg, st)
	r.logs.await(t, "identity schema incomplete, run /app migrate -apply")
	for range 3 {
		receive(t, st.reloads, "health check with an incomplete schema")
	}
	requireStatus(t, r.admin, "/readyz", http.StatusServiceUnavailable)
	requireSilent(t, "the directory load", st.slotAsked)
	if len(st.dueAsked) != 0 {
		t.Fatal("the signing key activation ran before the schema was complete")
	}
	requireSilent(t, "the revocation cleanup", st.cleaned)
	r.logs.mu.Lock()
	logged := r.logs.text.String()
	r.logs.mu.Unlock()
	if !strings.Contains(logged, "collection users absent") {
		t.Fatalf("logs lack the missing collection: %s", logged)
	}
	st.missing.Store(false)
	receive(t, st.slotAsked, "directory load after migrate")
	receive(t, st.dueAsked, "signing key activation after migrate")
	receive(t, st.cleaned, "revocation cleanup after migrate")
	r.logs.await(t, "identity ready")
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
}

func TestOutdatedIndexSetsTheGaugeAndStaysReady(t *testing.T) {
	kek := newKEK(t)
	st := &fakeStore{keys: []store.SigningKey{activeKey(t, kek)}, outdated: []string{"users"}}
	r := runIdentity(t, testConfig(kek), st)
	r.logs.await(t, "index outdated, run /app migrate -apply")
	r.logs.await(t, "identity ready")
	requireStatus(t, r.admin, "/readyz", http.StatusOK)
	requireMetrics(t, r.admin, `chatim_identity_index_outdated{collection="users"} 1`, `chatim_identity_index_outdated{collection="groups"} 0`,
		`chatim_identity_index_outdated{collection="slots"} 0`, `chatim_identity_index_outdated{collection="user_links"} 0`)
	_, body := httpGet(t, r.admin, "/metrics")
	if n := strings.Count(body, "chatim_identity_index_outdated{"); n != 10 {
		t.Fatalf("index_outdated series = %d, want 10", n)
	}
}

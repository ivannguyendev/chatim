package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

func openListener(t *testing.T, uri string) *pgstore.Listener {
	t.Helper()
	l, err := pgstore.OpenListener(context.Background(), uri)
	if err != nil {
		t.Fatalf("OpenListener: %v", err)
	}
	t.Cleanup(func() { _ = l.Close(context.Background()) })
	return l
}

func waitNotice(l *pgstore.Listener, timeout time.Duration) (store.Notice, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return l.Wait(ctx)
}

func wantNotice(t *testing.T, l *pgstore.Listener, want store.Notice) {
	t.Helper()
	got, err := waitNotice(l, time.Second)
	if err != nil {
		t.Fatalf("Wait: %v; want notice %v", err, want)
	}
	if got != want {
		t.Fatalf("Wait = %v; want %v", got, want)
	}
}

func setDisplayName(t *testing.T, s fixture, name string) {
	t.Helper()
	if err := s.SetProfile(context.Background(), "shop", lanID, store.ProfileUpdate{DisplayName: &name}, audit("profile_set")); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
}

func TestListenerWakesOnCommittedChange(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", lanID, "lan")
	l := openListener(t, s.uri)
	setDisplayName(t, s, "Lan Nguyen")
	wantNotice(t, l, store.NoticeDirectory)
}

func TestListenerSurvivesWaitTimeouts(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	seedAccount(t, s, "shop", lanID, "lan")
	l := openListener(t, s.uri)
	for i := range 3 {
		notice, err := waitNotice(l, 50*time.Millisecond)
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, store.ErrListenerBroken) {
			t.Fatalf("Wait %d = %v, %v; want a timeout", i, notice, err)
		}
	}
	setDisplayName(t, s, "Lan Nguyen")
	wantNotice(t, l, store.NoticeDirectory)
}

func TestListenerReportsRevocationNotices(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	lan := seedAccount(t, s, "shop", lanID, "lan")
	seedSessions(t, s, lan, phoneID)
	l := openListener(t, s.uri)
	revoked, err := s.RevokeSession(context.Background(), phoneID, revocation(), audit("session_revoke"))
	if err != nil || !revoked {
		t.Fatalf("RevokeSession = %v, %v; want true", revoked, err)
	}
	wantNotice(t, l, store.NoticeRevocations)
	exec(t, s.raw, "SELECT pg_notify('iam_changes', 'other')")
	wantNotice(t, l, store.NoticeOther)
}

func TestListenerReturnsAnErrorWhenTheBackendIsTerminated(t *testing.T) {
	s := openStore(t)
	l := openListener(t, s.uri)
	exec(t, s.raw, "SELECT pg_terminate_backend($1)", int64(l.BackendPID()))
	for i := range 2 {
		notice, err := waitNotice(l, 5*time.Second)
		if !errors.Is(err, store.ErrListenerBroken) || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait %d after terminate = %v, %v; want ErrListenerBroken", i, notice, err)
		}
	}
}

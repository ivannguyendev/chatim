package auth_test

import (
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth"
)

func TestRefusedNodeStaysNotReadyEvenWithTheSharedCopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		shared := &fakeShared{value: sharedValue(t, start, phoneRevoked(start.Add(16*time.Minute)))}
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusUnauthorized}}
		r := newRig(t, testSyncConfig(), doer, shared)
		r.start()
		defer r.stop()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		select {
		case <-r.syncer.Booted():
			t.Fatal("booted while identity refused the node secret")
		default:
		}
		if body := metricsText(t, r.syncer); !strings.Contains(body, `test_identity_refused{kind="revocations"} 401`) {
			t.Fatalf("metrics = %s", body)
		}
		doer.setRevoke(answer{status: http.StatusOK, body: snapshotBody(t, auth.RevocationSnapshot{})})
		time.Sleep(2 * time.Second)
		synctest.Wait()
		select {
		case <-r.syncer.Booted():
		default:
			t.Fatal("not booted after identity accepted the node")
		}
		if body := metricsText(t, r.syncer); !strings.Contains(body, `test_identity_refused{kind="revocations"} 0`) {
			t.Fatalf("metrics after accept = %s", body)
		}
	})
}

func TestMissingJWKSPathKeepsTheNodeNotReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		doer := &fakeDoer{keys: answer{status: http.StatusNotFound}, revoke: answer{status: http.StatusOK, body: snapshotBody(t, auth.RevocationSnapshot{})}}
		r := newRig(t, testSyncConfig(), doer, nil)
		r.start()
		defer r.stop()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		select {
		case <-r.syncer.Booted():
			t.Fatal("booted while the jwks url answered 404")
		default:
		}
		if body := metricsText(t, r.syncer); !strings.Contains(body, `test_identity_refused{kind="keys"} 404`) {
			t.Fatalf("metrics = %s", body)
		}
	})
}

func TestBusyIdentityStillBootsAfterTheTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusTooManyRequests}}
		r := newRig(t, testSyncConfig(), doer, nil)
		start := time.Now()
		r.start()
		defer r.stop()
		<-r.syncer.Booted()
		if waited := time.Since(start); waited != 10*time.Second {
			t.Fatalf("booted after %v; want the 10s ready timeout", waited)
		}
	})
}

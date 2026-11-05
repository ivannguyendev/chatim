package authtest_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth"
	"github.com/ivannguyendev/chatim/pkg/auth/authtest"
)

type keySink struct{ sets int }

func (k *keySink) SetDynamic([]byte) (int, error) { k.sets++; return 0, nil }

func TestAcceptingBootsAtOnceAndRefusingNever(t *testing.T) {
	cases := map[string]struct {
		doer   auth.Doer
		booted bool
	}{
		"accepting": {authtest.Accepting{}, true},
		"refusing":  {authtest.Refusing{Status: http.StatusUnauthorized}, false},
	}
	for name, tc := range cases {
		list, _ := auth.NewRevocationList(auth.RevocationListConfig{MaxKeep: time.Hour})
		cfg := authtest.SyncConfig()
		cfg.ReadyTimeout = 50 * time.Millisecond
		s, err := auth.NewSyncer(cfg, list, &keySink{}, nil, tc.doer, nil)
		if err != nil {
			t.Fatalf("%s: New: %v", name, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.Run(ctx)
		}()
		var booted bool
		select {
		case <-s.Booted():
			booted = true
		case <-time.After(500 * time.Millisecond):
		}
		cancel()
		<-done
		if booted != tc.booted {
			t.Fatalf("%s: booted = %v, want %v", name, booted, tc.booted)
		}
	}
}

package identitysynctest_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/identitysync"
	"github.com/ivannguyendev/chatim/pkg/identitysync/identitysynctest"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

type keySink struct{ sets int }

func (k *keySink) SetDynamic([]byte) (int, error) { k.sets++; return 0, nil }

func TestAcceptingBootsAtOnceAndRefusingNever(t *testing.T) {
	cases := map[string]struct {
		doer   identitysync.Doer
		booted bool
	}{
		"accepting": {identitysynctest.Accepting{}, true},
		"refusing":  {identitysynctest.Refusing{Status: http.StatusUnauthorized}, false},
	}
	for name, tc := range cases {
		list, _ := revocation.New(revocation.Config{MaxKeep: time.Hour})
		cfg := identitysynctest.Config()
		cfg.ReadyTimeout = 50 * time.Millisecond
		s, err := identitysync.New(cfg, list, &keySink{}, nil, tc.doer, nil)
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

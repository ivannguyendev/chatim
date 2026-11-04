package jsconsume_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

func TestNakDelayClimbsTheLadderAndStaysAtTheTop(t *testing.T) {
	cases := []struct {
		delivered uint64
		want      time.Duration
	}{
		{0, 5 * time.Second},
		{1, 5 * time.Second},
		{2, 30 * time.Second},
		{3, 2 * time.Minute},
		{4, 10 * time.Minute},
		{5, 10 * time.Minute},
		{1 << 63, 10 * time.Minute},
	}
	for _, c := range cases {
		if got := jsconsume.NakDelay(c.delivered); got != c.want {
			t.Errorf("NakDelay(%d) = %v, want %v", c.delivered, got, c.want)
		}
	}
}

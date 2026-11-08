package domain_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestOnlyCompletedStaysCompletedEverythingElseIsProcessing(t *testing.T) {
	cases := map[domain.Bucket]domain.Bucket{
		"":                      domain.BucketProcessing,
		domain.BucketProcessing: domain.BucketProcessing,
		domain.BucketCompleted:  domain.BucketCompleted,
	}
	for in, want := range cases {
		if got := in.OrProcessing(); got != want {
			t.Fatalf("%q.OrProcessing() = %q, want %q", in, got, want)
		}
	}
}

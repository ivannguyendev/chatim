package envconfig_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/pkg/core/envconfig"
)

func TestBodySizeMaxIsOneSharedKeyWithA128KiBDefault(t *testing.T) {
	if envconfig.BodySizeMaxKey != "APP_BODY_SIZE_MAX" || envconfig.DefaultBodySizeMax != 128<<10 {
		t.Fatalf("body size setting = %s default %d; want APP_BODY_SIZE_MAX default 131072", envconfig.BodySizeMaxKey, envconfig.DefaultBodySizeMax)
	}
}

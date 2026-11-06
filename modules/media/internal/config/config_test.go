package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/config"
)

func valid() config.Config {
	c := config.Defaults()
	c.Endpoint, c.PublicEndpoint, c.Region = "http://chatim-s3:3900", "https://media.example.com", "us-east-1"
	c.AccessKey, c.SecretKey = "GK1", "secret"
	c.ProcessingBucket, c.CompletedBucket = "p", "c"
	return c
}

func TestDefaultsMatchTheOwnerLimits(t *testing.T) {
	l := config.Defaults().Limits()
	if l.MinPartBytes != 16<<20 || l.MaxPartBytes != 128<<20 || l.MaxParts != 2000 || l.MaxParallel != 4 || l.MaxFileBytes != 4<<30 {
		t.Fatalf("limits %+v", l)
	}
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNamesTheBrokenSetting(t *testing.T) {
	cases := map[string]func(*config.Config){
		"MEDIA_S3_ENDPOINT":         func(c *config.Config) { c.Endpoint = "chatim-s3:3900" },
		"MEDIA_S3_PUBLIC_ENDPOINT":  func(c *config.Config) { c.PublicEndpoint = "" },
		"MEDIA_S3_REGION":           func(c *config.Config) { c.Region = "" },
		"MEDIA_S3_SECRET_KEY":       func(c *config.Config) { c.SecretKey = "" },
		"MEDIA_S3_BUCKET_COMPLETED": func(c *config.Config) { c.CompletedBucket = c.ProcessingBucket },
		"MEDIA_PART_URL_TTL":        func(c *config.Config) { c.PartSignatureTTL = 0 },
		"min part bytes":            func(c *config.Config) { c.MinPartBytes = 1 << 20 },
		"max file bytes":            func(c *config.Config) { c.MaxFileBytes = 2000*128<<20 + 1 },
	}
	for want, change := range cases {
		c := valid()
		change(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

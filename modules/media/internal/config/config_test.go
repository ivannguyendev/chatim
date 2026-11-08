package config_test

import (
	"strings"
	"testing"
	"time"

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
	d := config.Defaults()
	if d.DownloadSignatureTTL != time.Hour || d.ProcessingRetention != 720*time.Hour || d.Thumbnails || d.ThumbnailSuffix != ".thumb" {
		t.Fatalf("download defaults %+v", d)
	}
	if d.CopySingleMax != 5<<30 || d.CopyPartBytes != 1<<30 {
		t.Fatalf("copy defaults %+v", d.CopyLimits())
	}
	if d.FetchBatch != 8 || d.Workers != 4 || d.AckWait != 30*time.Second || d.SyncTimeout != 15*time.Minute {
		t.Fatalf("consumer defaults %+v", d)
	}
}

func TestConsumerSettingsMustLetTheConsumerRun(t *testing.T) {
	for name, change := range map[string]func(*config.Config){
		"no batch":     func(c *config.Config) { c.FetchBatch = 0 },
		"no workers":   func(c *config.Config) { c.Workers = 0 },
		"short ack":    func(c *config.Config) { c.AckWait = 2 * time.Second },
		"no sync time": func(c *config.Config) { c.SyncTimeout = 0 },
	} {
		c := valid()
		change(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "MEDIA_CONSUMER_") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCopyPartsMustCoverTheLargestFile(t *testing.T) {
	c := valid()
	c.MaxPartBytes, c.MaxParts, c.MaxFileBytes = 5<<30, 10000, 10000*(5<<20)+1
	c.CopyPartBytes = 5 << 20
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "MEDIA_COPY_PART_BYTES") {
		t.Fatalf("err = %v", err)
	}
	c.CopyPartBytes = 6 << 20
	if err := c.Validate(); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestThumbnailSuffixShape(t *testing.T) {
	for suffix, ok := range map[string]bool{
		".thumb": true, ".t": true, ".small.webp": true, "." + strings.Repeat("a", 31): true,
		"": false, ".": false, "thumb": false, ".Thumb": false, ".th/umb": false, ".th umb": false, "." + strings.Repeat("a", 32): false,
	} {
		c := valid()
		c.ThumbnailSuffix = suffix
		err := c.Validate()
		if (err == nil) != ok || (err != nil && !strings.Contains(err.Error(), "MEDIA_THUMBNAIL_SUFFIX")) {
			t.Errorf("suffix %q: err = %v, want ok %v", suffix, err, ok)
		}
	}
}

func TestValidateNamesTheBrokenSetting(t *testing.T) {
	cases := map[string]func(*config.Config){
		"MEDIA_S3_ENDPOINT":          func(c *config.Config) { c.Endpoint = "chatim-s3:3900" },
		"MEDIA_S3_PUBLIC_ENDPOINT":   func(c *config.Config) { c.PublicEndpoint = "" },
		"MEDIA_S3_REGION":            func(c *config.Config) { c.Region = "" },
		"MEDIA_S3_SECRET_KEY":        func(c *config.Config) { c.SecretKey = "" },
		"MEDIA_S3_BUCKET_COMPLETED":  func(c *config.Config) { c.CompletedBucket = c.ProcessingBucket },
		"MEDIA_PART_URL_TTL":         func(c *config.Config) { c.PartSignatureTTL = 0 },
		"MEDIA_DOWNLOAD_URL_TTL":     func(c *config.Config) { c.DownloadSignatureTTL = 0 },
		"MEDIA_PROCESSING_RETENTION": func(c *config.Config) { c.ProcessingRetention = -time.Hour },
		"min part bytes":             func(c *config.Config) { c.MinPartBytes = 1 << 20 },
		"max file bytes":             func(c *config.Config) { c.MaxFileBytes = 2000*128<<20 + 1 },
		"MEDIA_COPY_SINGLE_MAX":      func(c *config.Config) { c.CopySingleMax = 5<<30 + 1 },
		"MEDIA_COPY_PART_BYTES":      func(c *config.Config) { c.CopyPartBytes = 5<<20 - 1 },
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

func TestSignatureTTLsStayWithinS3Limits(t *testing.T) {
	cases := []struct {
		want   string
		change func(*config.Config)
	}{
		{"MEDIA_PART_URL_TTL", func(c *config.Config) { c.PartSignatureTTL = 500 * time.Millisecond }},
		{"MEDIA_PART_URL_TTL", func(c *config.Config) { c.PartSignatureTTL = 7*24*time.Hour + time.Second }},
		{"MEDIA_DOWNLOAD_URL_TTL", func(c *config.Config) { c.DownloadSignatureTTL = 999 * time.Millisecond }},
		{"MEDIA_DOWNLOAD_URL_TTL", func(c *config.Config) { c.DownloadSignatureTTL = 240 * time.Hour }},
	}
	for _, tc := range cases {
		c := valid()
		tc.change(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", tc.want, err)
		}
	}
	c := valid()
	c.PartSignatureTTL, c.DownloadSignatureTTL = time.Second, 7*24*time.Hour
	if err := c.Validate(); err != nil {
		t.Fatalf("bounds: err = %v, want nil", err)
	}
}

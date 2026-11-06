package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

const (
	DefaultMaxFileBytes       = 4 << 30
	DefaultMinPartBytes       = 16 << 20
	DefaultMaxPartBytes       = 128 << 20
	DefaultMaxParts           = 2000
	DefaultMaxParallelUploads = 4
	DefaultPartSignatureTTL   = 15 * time.Minute
	DefaultReadyCheckInterval = 30 * time.Second
)

type Config struct {
	Enabled            bool
	Endpoint           string
	PublicEndpoint     string
	Region             string
	AccessKey          string
	SecretKey          string
	ProcessingBucket   string
	CompletedBucket    string
	MaxFileBytes       int64
	MinPartBytes       int64
	MaxPartBytes       int64
	MaxParts           int
	MaxParallelUploads int
	PartSignatureTTL   time.Duration
	ReadyCheckInterval time.Duration
}

func Defaults() Config {
	return Config{
		Enabled: true, MaxFileBytes: DefaultMaxFileBytes, MinPartBytes: DefaultMinPartBytes, MaxPartBytes: DefaultMaxPartBytes,
		MaxParts: DefaultMaxParts, MaxParallelUploads: DefaultMaxParallelUploads,
		PartSignatureTTL: DefaultPartSignatureTTL, ReadyCheckInterval: DefaultReadyCheckInterval,
	}
}

func (c Config) Limits() domain.Limits {
	return domain.Limits{
		MinPartBytes: c.MinPartBytes, MaxPartBytes: c.MaxPartBytes, MaxParts: c.MaxParts,
		MaxParallel: c.MaxParallelUploads, MaxFileBytes: c.MaxFileBytes,
	}
}

func (c Config) Validate() error {
	var errs []error
	for name, endpoint := range map[string]string{"MEDIA_S3_ENDPOINT": c.Endpoint, "MEDIA_S3_PUBLIC_ENDPOINT": c.PublicEndpoint} {
		if u, err := url.Parse(endpoint); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, fmt.Errorf("%s must be an http or https URL", name))
		}
	}
	switch {
	case c.Region == "":
		errs = append(errs, errors.New("MEDIA_S3_REGION is required"))
	case c.AccessKey == "" || c.SecretKey == "":
		errs = append(errs, errors.New("MEDIA_S3_ACCESS_KEY and MEDIA_S3_SECRET_KEY are required"))
	case c.ProcessingBucket == "" || c.CompletedBucket == "" || c.ProcessingBucket == c.CompletedBucket:
		errs = append(errs, errors.New("MEDIA_S3_BUCKET_PROCESSING and MEDIA_S3_BUCKET_COMPLETED must be set and differ"))
	}
	if c.PartSignatureTTL <= 0 || c.ReadyCheckInterval <= 0 {
		errs = append(errs, errors.New("MEDIA_PART_URL_TTL and MEDIA_READY_CHECK_INTERVAL must be positive"))
	}
	if err := c.Limits().Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

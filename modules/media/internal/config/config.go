package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
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
	DefaultDownloadTTL        = time.Hour
	DefaultRetention          = 30 * 24 * time.Hour
	minSignatureTTL           = time.Second
	maxSignatureTTL           = 7 * 24 * time.Hour
	DefaultThumbnailSuffix    = ".thumb"
	minThumbnailSuffix        = 2
	maxThumbnailSuffix        = 32
	thumbnailSuffixAlphabet   = "abcdefghijklmnopqrstuvwxyz0123456789."
)

type Config struct {
	Enabled              bool
	Endpoint             string
	PublicEndpoint       string
	Region               string
	AccessKey            string
	SecretKey            string
	ProcessingBucket     string
	CompletedBucket      string
	MaxFileBytes         int64
	MinPartBytes         int64
	MaxPartBytes         int64
	MaxParts             int
	MaxParallelUploads   int
	PartSignatureTTL     time.Duration
	ReadyCheckInterval   time.Duration
	DownloadSignatureTTL time.Duration
	ProcessingRetention  time.Duration
	Thumbnails           bool
	ThumbnailSuffix      string
}

func Defaults() Config {
	return Config{
		Enabled: true, MaxFileBytes: DefaultMaxFileBytes, MinPartBytes: DefaultMinPartBytes, MaxPartBytes: DefaultMaxPartBytes,
		MaxParts: DefaultMaxParts, MaxParallelUploads: DefaultMaxParallelUploads,
		PartSignatureTTL: DefaultPartSignatureTTL, ReadyCheckInterval: DefaultReadyCheckInterval,
		DownloadSignatureTTL: DefaultDownloadTTL, ProcessingRetention: DefaultRetention, ThumbnailSuffix: DefaultThumbnailSuffix,
	}
}

func (c Config) ThumbnailRule() domain.Thumbnails {
	return domain.Thumbnails{Enabled: c.Thumbnails, Suffix: c.ThumbnailSuffix}
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
	if c.DownloadSignatureTTL <= 0 || c.ProcessingRetention <= 0 {
		errs = append(errs, errors.New("MEDIA_DOWNLOAD_URL_TTL and MEDIA_PROCESSING_RETENTION must be positive"))
	}
	errs = append(errs, signatureTTLError("MEDIA_PART_URL_TTL", c.PartSignatureTTL), signatureTTLError("MEDIA_DOWNLOAD_URL_TTL", c.DownloadSignatureTTL))
	if !validThumbnailSuffix(c.ThumbnailSuffix) {
		errs = append(errs, fmt.Errorf("MEDIA_THUMBNAIL_SUFFIX must start with a dot and be %d..%d characters of a-z, 0-9 and dots", minThumbnailSuffix, maxThumbnailSuffix))
	}
	if err := c.Limits().Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func validThumbnailSuffix(s string) bool {
	return len(s) >= minThumbnailSuffix && len(s) <= maxThumbnailSuffix && strings.HasPrefix(s, ".") && strings.Trim(s, thumbnailSuffixAlphabet) == ""
}

func signatureTTLError(name string, ttl time.Duration) error {
	if ttl < minSignatureTTL || ttl > maxSignatureTTL {
		return fmt.Errorf("%s must be %v..%v, the range S3 accepts for a signed URL", name, minSignatureTTL, maxSignatureTTL)
	}
	return nil
}

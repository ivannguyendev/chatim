package config

import (
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/envconfig"
)

func (p *parser) mediaConfig(c *Config) {
	m := mediaapp.DefaultConfig()
	m.Enabled = p.flag("MEDIA_ENABLED", true)
	m.Endpoint = envconfig.String("MEDIA_S3_ENDPOINT", "")
	m.PublicEndpoint = envconfig.String("MEDIA_S3_PUBLIC_ENDPOINT", "")
	m.Region = envconfig.String("MEDIA_S3_REGION", "")
	m.AccessKey = envconfig.String("MEDIA_S3_ACCESS_KEY", "")
	m.SecretKey = p.secret("MEDIA_S3_SECRET_KEY")
	m.ProcessingBucket = envconfig.String("MEDIA_S3_BUCKET_PROCESSING", "")
	m.CompletedBucket = envconfig.String("MEDIA_S3_BUCKET_COMPLETED", "")
	m.MaxFileBytes = int64(p.count("MEDIA_MAX_FILE_BYTES", int(m.MaxFileBytes)))
	m.MinPartBytes = int64(p.count("MEDIA_MIN_PART_BYTES", int(m.MinPartBytes)))
	m.MaxPartBytes = int64(p.count("MEDIA_MAX_PART_BYTES", int(m.MaxPartBytes)))
	m.MaxParts = p.count("MEDIA_MAX_PARTS", m.MaxParts)
	m.MaxParallelUploads = p.count("MEDIA_MAX_PARALLEL_UPLOADS", m.MaxParallelUploads)
	m.PartSignatureTTL = p.span("MEDIA_PART_URL_TTL", m.PartSignatureTTL)
	m.ReadyCheckInterval = p.span("MEDIA_READY_CHECK_INTERVAL", m.ReadyCheckInterval)
	m.DownloadSignatureTTL = p.span("MEDIA_DOWNLOAD_URL_TTL", m.DownloadSignatureTTL)
	m.ProcessingRetention = p.span("MEDIA_PROCESSING_RETENTION", m.ProcessingRetention)
	m.Thumbnails = p.flag("MEDIA_THUMBNAILS", m.Thumbnails)
	m.ThumbnailSuffix = envconfig.String("MEDIA_THUMBNAIL_SUFFIX", m.ThumbnailSuffix)
	c.Media = m
}

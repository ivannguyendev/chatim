package attach

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type Kind uint8

const (
	Created Kind = iota + 1
	Deleted
)

const UploadExpiry = 24 * time.Hour

var (
	ErrDropped    = errors.New("attach: event dropped")
	ErrWaitUpload = errors.New("attach: a file of the message is still uploading")
)

type Event struct {
	Tenant   string
	Ref      domain.MessageRef
	HasFiles bool
}

type Chat interface {
	MessageFiles(ctx context.Context, ref domain.MessageRef) (domain.MessageState, error)
}

type Uploads interface {
	GetMany(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error)
	MarkMoved(ctx context.Context, key string, ver int64, at time.Time) error
	Retire(ctx context.Context, keys []string, at time.Time) error
	Unretire(ctx context.Context, keys []string) error
}

type Objects interface {
	Copy(ctx context.Context, key string, size int64, contentType string) error
	Stat(ctx context.Context, bucket domain.Bucket, key string) (domain.Object, bool, error)
	DeleteProcessing(ctx context.Context, key string) error
}

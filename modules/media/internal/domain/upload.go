package domain

import "time"

type State int32

const (
	StateUploading State = 1
	StateUploaded  State = 2
	StateAborted   State = 3
)

type Upload struct {
	Key         string
	Tenant      string
	User        string
	FileName    string
	ContentType string
	Size        int64
	Dim         []int32
	Dur         int64
	Hash        string
	PartSize    int64
	PartCount   int
	UploadID    string
	State       State
	Ver         int64
	CreatedAt   time.Time
	CompletedAt time.Time
	UpdatedAt   time.Time
}

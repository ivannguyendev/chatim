package domain

type Bucket string

const (
	BucketProcessing Bucket = "processing"
	BucketCompleted  Bucket = "completed"
)

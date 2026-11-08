package domain

type Bucket string

const (
	BucketProcessing Bucket = "processing"
	BucketCompleted  Bucket = "completed"
)

type Object struct {
	Size        int64
	ContentType string
}

func (b Bucket) OrProcessing() Bucket {
	if b == BucketCompleted {
		return b
	}
	return BucketProcessing
}

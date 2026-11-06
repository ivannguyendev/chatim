package domain

import "fmt"

const (
	MiB             = int64(1) << 20
	s3MinPartBytes  = 5 * MiB
	s3MaxPartCount  = 10000
	partSeconds     = 8
	defaultPartSize = 32 * MiB
)

type Limits struct {
	MinPartBytes int64
	MaxPartBytes int64
	MaxParts     int
	MaxParallel  int
	MaxFileBytes int64
}

func (l Limits) Validate() error {
	switch {
	case l.MinPartBytes < s3MinPartBytes:
		return fmt.Errorf("min part bytes %d below the S3 floor %d", l.MinPartBytes, s3MinPartBytes)
	case l.MaxPartBytes < l.MinPartBytes:
		return fmt.Errorf("max part bytes %d below min part bytes %d", l.MaxPartBytes, l.MinPartBytes)
	case l.MaxParts < 1 || l.MaxParts > s3MaxPartCount:
		return fmt.Errorf("max parts %d outside 1..%d", l.MaxParts, s3MaxPartCount)
	case l.MaxParallel < 1 || l.MaxParallel > l.MaxParts:
		return fmt.Errorf("max parallel uploads %d outside 1..%d", l.MaxParallel, l.MaxParts)
	case l.MaxFileBytes < 1 || l.MaxFileBytes > int64(l.MaxParts)*l.MaxPartBytes:
		return fmt.Errorf("max file bytes %d outside 1..%d", l.MaxFileBytes, int64(l.MaxParts)*l.MaxPartBytes)
	}
	return nil
}

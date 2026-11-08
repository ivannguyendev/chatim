package domain

import "fmt"

const s3MaxCopyBytes = 5 << 30

type CopyLimits struct {
	SingleMax int64
	PartBytes int64
}

type ByteRange struct {
	Start  int64
	Length int64
}

func (l CopyLimits) Validate() error {
	switch {
	case l.SingleMax <= 0 || l.SingleMax > s3MaxCopyBytes:
		return fmt.Errorf("copy single max %d outside 1..%d", l.SingleMax, int64(s3MaxCopyBytes))
	case l.PartBytes < s3MinPartBytes || l.PartBytes > s3MaxCopyBytes:
		return fmt.Errorf("copy part bytes %d outside %d..%d", l.PartBytes, s3MinPartBytes, int64(s3MaxCopyBytes))
	}
	return nil
}

func (l CopyLimits) Whole(size int64) bool { return size <= l.SingleMax }

func (l CopyLimits) Parts(size int64) ([]ByteRange, error) {
	if size <= 0 || l.PartBytes <= 0 {
		return nil, fmt.Errorf("copy %d bytes in %d-byte parts", size, l.PartBytes)
	}
	count := (size + l.PartBytes - 1) / l.PartBytes
	if count > s3MaxPartCount {
		return nil, fmt.Errorf("copy %d bytes needs %d parts, over %d", size, count, s3MaxPartCount)
	}
	out := make([]ByteRange, 0, count)
	for start := int64(0); start < size; start += l.PartBytes {
		out = append(out, ByteRange{Start: start, Length: min(l.PartBytes, size-start)})
	}
	return out, nil
}

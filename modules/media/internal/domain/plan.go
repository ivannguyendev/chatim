package domain

import "fmt"

type Part struct {
	Number int32
	Size   int64
}

func PlanParts(size, bytesPerSecond int64, l Limits) (int64, []Part, error) {
	if size < 1 || size > l.MaxFileBytes {
		return 0, nil, fmt.Errorf("%w: size %d outside 1..%d", ErrInvalid, size, l.MaxFileBytes)
	}
	streams := int64(l.MaxParallel)
	target := clamp(targetPartSize(bytesPerSecond), l.MinPartBytes, l.MaxPartBytes)
	rounds := (size + streams*target/2) / (streams * target)
	rounds = max(rounds, ceilDiv(size, streams*l.MaxPartBytes), 1)
	rounds = min(rounds, int64(l.MaxParts)/streams)
	partSize := clamp(ceilDiv(ceilDiv(size, streams*rounds), MiB)*MiB, l.MinPartBytes, l.MaxPartBytes)
	count := ceilDiv(size, partSize)
	if count > int64(l.MaxParts) {
		return 0, nil, fmt.Errorf("%w: size %d needs %d parts, over %d", ErrInvalid, size, count, l.MaxParts)
	}
	parts := make([]Part, count)
	for i := range parts {
		parts[i] = Part{Number: int32(i + 1), Size: min(partSize, size-int64(i)*partSize)}
	}
	return partSize, parts, nil
}

func targetPartSize(bytesPerSecond int64) int64 {
	switch {
	case bytesPerSecond <= 0:
		return defaultPartSize
	case bytesPerSecond > (1<<62)/partSeconds:
		return 1 << 62
	}
	return bytesPerSecond * partSeconds
}

func clamp(v, lo, hi int64) int64 { return min(max(v, lo), hi) }

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

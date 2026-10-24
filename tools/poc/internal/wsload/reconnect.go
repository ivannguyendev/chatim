package wsload

import "time"

func ReconnectDelay(maxDelay time.Duration, int64n func(int64) int64) time.Duration {
	if maxDelay <= 0 {
		return 0
	}
	return time.Duration(int64n(int64(maxDelay) + 1))
}

func OtherURL(urls, current int, intn func(int) int) int {
	if urls <= 1 {
		return 0
	}
	next := intn(urls - 1)
	if next >= current {
		next++
	}
	return next
}

package wsclient

import (
	"bytes"
	"runtime"
	"strconv"
)

func goroutineID() uint64 {
	var buf [64]byte
	stack := bytes.TrimPrefix(buf[:runtime.Stack(buf[:], false)], []byte("goroutine "))
	field, _, found := bytes.Cut(stack, []byte(" "))
	if !found {
		return 0
	}
	id, err := strconv.ParseUint(string(field), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

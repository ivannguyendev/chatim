package password

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	phcAlgorithm   = "argon2id"
	phcVersion     = "v=19"
	maxMemory      = 262144
	maxTime        = 10
	maxThreads     = 8
	minSaltLength  = 8
	maxSaltLength  = 64
	minKeyLength   = 16
	maxKeyLength   = 64
	phcFieldCount  = 6
	paramPairCount = 3
)

var ErrMalformedHash = errors.New("malformed password hash")

type Params struct {
	Memory     uint32
	Time       uint32
	Threads    uint8
	SaltLength uint32
	KeyLength  uint32
}

var DefaultParams = Params{Memory: 19456, Time: 2, Threads: 1, SaltLength: 16, KeyLength: 32}

type decoded struct {
	params Params
	salt   []byte
	key    []byte
}

func (p Params) validate() error {
	switch {
	case p.Memory == 0 || p.Memory > maxMemory:
		return fmt.Errorf("memory %d out of range", p.Memory)
	case p.Time == 0 || p.Time > maxTime:
		return fmt.Errorf("time %d out of range", p.Time)
	case p.Threads == 0 || p.Threads > maxThreads:
		return fmt.Errorf("threads %d out of range", p.Threads)
	case p.SaltLength < minSaltLength || p.SaltLength > maxSaltLength:
		return fmt.Errorf("salt length %d out of range", p.SaltLength)
	case p.KeyLength < minKeyLength || p.KeyLength > maxKeyLength:
		return fmt.Errorf("key length %d out of range", p.KeyLength)
	}
	return nil
}

func encode(p Params, salt, key []byte) string {
	return fmt.Sprintf("$%s$%s$m=%d,t=%d,p=%d$%s$%s",
		phcAlgorithm, phcVersion, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

func decode(s string) (decoded, error) {
	fields := strings.Split(s, "$")
	if len(fields) != phcFieldCount || fields[0] != "" || fields[1] != phcAlgorithm || fields[2] != phcVersion {
		return decoded{}, ErrMalformedHash
	}
	p, err := parseParams(fields[3])
	if err != nil {
		return decoded{}, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil {
		return decoded{}, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil {
		return decoded{}, ErrMalformedHash
	}
	p.SaltLength = byteLength(salt)
	p.KeyLength = byteLength(key)
	if p.validate() != nil {
		return decoded{}, ErrMalformedHash
	}
	return decoded{params: p, salt: salt, key: key}, nil
}

func parseParams(s string) (Params, error) {
	pairs := strings.Split(s, ",")
	if len(pairs) != paramPairCount {
		return Params{}, ErrMalformedHash
	}
	memory, okMemory := parseField(pairs[0], "m=")
	time, okTime := parseField(pairs[1], "t=")
	threads, okThreads := parseField(pairs[2], "p=")
	if !okMemory || !okTime || !okThreads || threads > math.MaxUint8 {
		return Params{}, ErrMalformedHash
	}
	return Params{Memory: memory, Time: time, Threads: uint8(threads)}, nil
}

func parseField(pair, name string) (uint32, bool) {
	raw, ok := strings.CutPrefix(pair, name)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || v > math.MaxUint32 {
		return 0, false
	}
	return uint32(v), true
}

func byteLength(b []byte) uint32 {
	n := len(b)
	if n > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}

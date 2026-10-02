package openloop

import (
	"errors"
	"math/rand/v2"
	"sync"
)

type Picker struct {
	mu   sync.Mutex
	rng  *rand.Rand
	zipf *rand.Zipf
	n    int
}

func NewPicker(rooms int, zipfS float64, seed uint64) (*Picker, error) {
	switch {
	case rooms <= 0:
		return nil, errors.New("openloop: picker needs at least one room")
	case zipfS != 0 && zipfS <= 1:
		return nil, errors.New("openloop: zipf exponent must be above 1, or 0 for uniform")
	}
	p := &Picker{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), n: rooms}
	if zipfS != 0 {
		p.zipf = rand.NewZipf(p.rng, zipfS, 1, uint64(rooms-1))
	}
	return p, nil
}

func (p *Picker) Next() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.zipf != nil {
		return int(p.zipf.Uint64())
	}
	return p.rng.IntN(p.n)
}

package wsload

type SeqSet struct {
	words []uint64
}

func (s *SeqSet) Add(seq uint64) bool {
	word, bit := seq/64, uint64(1)<<(seq%64)
	if word >= uint64(len(s.words)) {
		grown := make([]uint64, max(word+1, uint64(2*len(s.words))))
		copy(grown, s.words)
		s.words = grown
	}
	if s.words[word]&bit != 0 {
		return false
	}
	s.words[word] |= bit
	return true
}

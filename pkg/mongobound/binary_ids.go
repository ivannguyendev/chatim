package mongobound

import (
	"bytes"
	"cmp"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var binaryOrder = keyOrder[[]byte]{compare: compareBinData, successor: isSuccessor}

func IDs(ids [][]byte, extra ...bson.E) bson.D {
	return binaryOrder.filter(ids, extra)
}

func Runs(ids [][]byte) []Run[[]byte] {
	return binaryOrder.runs(ids)
}

func compareBinData(a, b []byte) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), bytes.Compare(a, b))
}

func isSuccessor(prev, next []byte) bool {
	if len(prev) != len(next) {
		return false
	}
	succ := bytes.Clone(prev)
	for i := len(succ) - 1; i >= 0; i-- {
		if succ[i] < 0xff {
			succ[i]++
			return bytes.Equal(succ, next)
		}
		succ[i] = 0
	}
	return false
}

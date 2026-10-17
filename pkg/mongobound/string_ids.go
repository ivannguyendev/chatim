package mongobound

import (
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var stringOrder = keyOrder[string]{compare: strings.Compare, successor: isNextString}

func StringIDs(ids []string, extra ...bson.E) bson.D {
	return stringOrder.filter(ids, extra)
}

func isNextString(prev, next string) bool {
	return len(next) == len(prev)+1 && next[len(prev)] == 0 && strings.HasPrefix(next, prev)
}

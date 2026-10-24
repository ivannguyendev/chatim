package typing

import (
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const subjectTokens = 4

var decodeOptions = proto.UnmarshalOptions{DiscardUnknown: true}

func decodeSignal(in inbound) (*chatimv1.TypingSignal, bool) {
	var sig chatimv1.TypingSignal
	if err := decodeOptions.Unmarshal(in.data, &sig); err != nil {
		return nil, false
	}
	tokens := strings.Split(in.subject, ".")
	if len(tokens) != subjectTokens || tokens[0] != subjectRoot || tokens[3] != subjectKind {
		return nil, false
	}
	room, err := ids.ParseRoomID(tokens[2])
	switch {
	case err != nil, room != sig.GetRoomId(), tokens[1] != sig.GetTenant():
		return nil, false
	case ids.ValidTenant(sig.GetTenant()) != nil, ids.ValidUser(sig.GetUserId()) != nil:
		return nil, false
	}
	return &sig, true
}

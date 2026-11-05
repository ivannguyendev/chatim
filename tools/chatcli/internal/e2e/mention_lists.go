package e2e

import (
	"fmt"
	"slices"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MentionOf(items []*chatimv1.MentionItem, at Place) (found, read bool) {
	for _, it := range items {
		if m := it.GetMessage(); m.GetRoomId() == at.Room && m.GetSeq() == at.Seq {
			return true, it.GetRead()
		}
	}
	return false, false
}

func CheckChanged(what string, got []string, want ...string) error {
	g, w := slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if !slices.Equal(g, w) {
		return fmt.Errorf("%s changed %v, want %v", what, g, w)
	}
	return nil
}

func FormatRoomGroups(groups []*chatimv1.RoomGroup) string {
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = g.GetId() + "=" + g.GetName()
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

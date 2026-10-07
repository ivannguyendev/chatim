package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoEditsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunEdits(t, func(t *testing.T) (storetest.EditableMessages, storetest.ClearableRooms, store.Edits, store.Hidden) {
		s, _ := itStore(t, client)
		return s, s, s, s.Hidden()
	})
}

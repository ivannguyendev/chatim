package sweep

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type RebuilderStores struct {
	Messages     EditedMessages
	Rooms        eventkit.RoomReader
	Edits        EditFacts
	Interactions InteractionReader
	Pins         PinFacts
	PinProjector PinProjecter
	Members      MemberLookup
	Hidden       HiddenLookup
}

func (s RebuilderStores) complete() bool {
	return s.Messages != nil && s.Rooms != nil && s.Edits != nil && s.Interactions != nil &&
		s.Pins != nil && s.PinProjector != nil && s.Members != nil && s.Hidden != nil
}

func NewRebuilders(s RebuilderStores, types *eventkit.RoomTypes, now func() time.Time) (map[store.StateCollection]Rebuilder, error) {
	if !s.complete() || types == nil {
		return nil, fmt.Errorf("%w: sweep rebuilders need every store and a room type cache", apperr.ErrInvalidArgument)
	}
	return map[store.StateCollection]Rebuilder{
		store.StateMessages:     NewMessageRebuilder(s.Messages, types, now),
		store.StateEdits:        NewEditRebuilder(s.Edits, s.Messages, types),
		store.StateInteractions: NewInteractionRebuilder(s.Interactions, types),
		store.StatePins:         NewPinRebuilder(s.PinProjector, s.Pins, s.Messages, types),
		store.StateMembers:      NewMemberRebuilder(s.Members, types, now),
		store.StateHidden:       NewHiddenRebuilder(s.Hidden, types),
		store.StateRooms:        NewRoomRebuilder(s.Rooms, now),
	}, nil
}

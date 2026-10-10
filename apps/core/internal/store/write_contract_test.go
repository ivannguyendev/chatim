package store_test

import (
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var allowedKinds = map[string]bool{
	"read": true, "lifecycle": true, "reset": true, "purge": true,
	"insert-unique": true, "cas": true, "monotonic-cas": true, "monotonic-max": true, "upsert": true, "version-bump": true, "transaction": true,
}

var portMethods = map[string]string{
	"Messages.Insert":     "insert-unique",
	"Messages.Last":       "read",
	"Messages.Page":       "read",
	"Messages.Find":       "read",
	"Rooms.Create":        "insert-unique",
	"Rooms.Get":           "read",
	"Rooms.Member":        "read",
	"Rooms.TouchActivity": "monotonic-max",
	"Rooms.ActiveRooms":   "read",
	"ChangeFeed.Open":     "read",
	"ChangeFeed.Forget":   "reset",
	"Cursor.Next":         "read",
	"Cursor.Confirm":      "monotonic-cas",
	"Cursor.Close":        "lifecycle",

	"MessageEditor.ApplyEdit":     "cas",
	"HistoryClearer.ClearHistory": "monotonic-max",
	"Edits.Append":                "insert-unique",
	"Edits.At":                    "read",
	"Edits.Latest":                "read",
	"Edits.History":               "read",
	"Edits.Between":               "read",
	"Edits.PurgeText":             "purge",
	"Hidden.Hide":                 "upsert",
	"Hidden.HiddenIn":             "read",
	"Hidden.Get":                  "read",
	"Hidden.Between":              "read",

	"Interactions.SetReaction":       "version-bump",
	"Interactions.RemoveReaction":    "version-bump",
	"Interactions.GetReaction":       "read",
	"Interactions.CountReactions":    "read",
	"Interactions.CountWitnessed":    "read",
	"Interactions.SetBookmark":       "version-bump",
	"Interactions.GetBookmark":       "read",
	"Interactions.Bookmarks":         "read",
	"Interactions.AddReply":          "upsert",
	"Interactions.RemoveReply":       "version-bump",
	"Interactions.Replies":           "read",
	"Interactions.CountLiveReplies":  "read",
	"Interactions.Between":           "read",
	"ReactionSummaries.SetReactions": "cas",
	"Pins.Append":                    "insert-unique",
	"Pins.At":                        "read",
	"Pins.After":                     "read",
	"Pins.Between":                   "read",
	"PinProjector.PinState":          "read",
	"PinProjector.ApplyPins":         "cas",

	"MemberWriter.AddMembers":     "version-bump",
	"MemberWriter.ApplyMember":    "cas",
	"MemberReader.MembersOf":      "read",
	"MemberReader.MembersBetween": "read",
	"OwnerChanges.ChangeOwners":   "transaction",
	"MemberCounts.AddMemberCount": "version-bump",
	"MemberCounts.CountMembers":   "read",
	"MemberCounts.SetMemberCount": "cas",
	"ReadPositions.MarkRead":      "version-bump",
	"ReadPositions.MarkUnread":    "version-bump",
}

func TestEveryPortMethodHasAWriteContract(t *testing.T) {
	ports := []reflect.Type{
		reflect.TypeFor[store.Messages](),
		reflect.TypeFor[store.Rooms](),
		reflect.TypeFor[store.ChangeFeed](),
		reflect.TypeFor[store.Cursor](),
		reflect.TypeFor[store.MessageEditor](),
		reflect.TypeFor[store.HistoryClearer](),
		reflect.TypeFor[store.Edits](),
		reflect.TypeFor[store.Hidden](),
		reflect.TypeFor[store.Interactions](),
		reflect.TypeFor[store.ReactionSummaries](),
		reflect.TypeFor[store.Pins](),
		reflect.TypeFor[store.PinProjector](),
		reflect.TypeFor[store.MemberWriter](),
		reflect.TypeFor[store.MemberReader](),
		reflect.TypeFor[store.OwnerChanges](),
		reflect.TypeFor[store.MemberCounts](),
		reflect.TypeFor[store.ReadPositions](),
	}
	seen := map[string]bool{}
	for _, p := range ports {
		for m := range p.Methods() {
			name := p.Name() + "." + m.Name
			seen[name] = true
			kind, ok := portMethods[name]
			switch {
			case !ok:
				t.Errorf("%s has no write contract: classify it here and add its storetest case", name)
			case !allowedKinds[kind]:
				t.Errorf("%s has kind %q; writes must be insert-unique, cas, monotonic-cas, monotonic-max, upsert, version-bump, purge or transaction", name, kind)
			}
		}
	}
	for name := range portMethods {
		if !seen[name] {
			t.Errorf("%s is classified but is not a port method", name)
		}
	}
}

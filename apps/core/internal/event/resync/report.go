package resync

import "fmt"

type Report struct {
	Rooms, RoomsCreated, MemberCounts, RoomInfos, Messages, Edits, Reactions, Bookmarks, Pins int
	Members, ReadPositions, HistoryCleared, Preferences, Hidden, CountChecks                  int
	DryRun                                                                                    bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d marked rooms.created=%d rooms.member_count=%d rooms.info=%d messages=%d message_edits=%d message_interactions.reaction=%d message_interactions.bookmark=%d pin_actions=%d members=%d members.read=%d members.cleared=%d members.preferences=%d hidden=%d count_checks=%d dry_run=%t",
		r.Rooms, r.RoomsCreated, r.MemberCounts, r.RoomInfos, r.Messages, r.Edits, r.Reactions, r.Bookmarks, r.Pins,
		r.Members, r.ReadPositions, r.HistoryCleared, r.Preferences, r.Hidden, r.CountChecks, r.DryRun)
}

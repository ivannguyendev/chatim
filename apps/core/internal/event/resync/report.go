package resync

import "fmt"

type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords, ReactionRecords, BookmarkRecords, PinRecords int
	MemberRecords, HiddenRecords, CountCheckRecords                                               int
	DryRun                                                                                        bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d edit_records=%d reaction_records=%d bookmark_records=%d pin_records=%d member_records=%d hidden_records=%d count_check_records=%d dry_run=%t",
		r.Rooms, r.RoomRecords, r.MessageRecords, r.EditRecords, r.ReactionRecords, r.BookmarkRecords, r.PinRecords, r.MemberRecords, r.HiddenRecords, r.CountCheckRecords, r.DryRun)
}

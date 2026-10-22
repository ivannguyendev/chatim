package command_test

const approvedClientFields = `
chatim.v1.AddMembersRequest 1 room_id single string
chatim.v1.AddMembersRequest 2 users repeated string
chatim.v1.AddMembersRequest 3 request_id single string
chatim.v1.ChangeMemberRoleRequest 1 room_id single string
chatim.v1.ChangeMemberRoleRequest 2 user single string
chatim.v1.ChangeMemberRoleRequest 3 role single enum chatim.v1.MemberRole
chatim.v1.ClearHistoryRequest 1 room_id single string
chatim.v1.CreateRoomRequest 1 type single enum chatim.v1.RoomType
chatim.v1.CreateRoomRequest 2 name single string
chatim.v1.CreateRoomRequest 3 members repeated string
chatim.v1.CreateRoomRequest 4 request_id single string
chatim.v1.DeleteMessageRequest 1 room_id single string
chatim.v1.DeleteMessageRequest 2 thread_root single uint64
chatim.v1.DeleteMessageRequest 3 seq single uint64
chatim.v1.DeleteMessageRequest 4 base_ver single uint32
chatim.v1.EditMessageRequest 1 room_id single string
chatim.v1.EditMessageRequest 2 thread_root single uint64
chatim.v1.EditMessageRequest 3 seq single uint64
chatim.v1.EditMessageRequest 4 base_ver single uint32
chatim.v1.EditMessageRequest 5 text single string
chatim.v1.EditMessageRequest 6 mentions single message chatim.v1.MentionSet
chatim.v1.ForwardRef 1 room_id single string
chatim.v1.ForwardRef 2 thread_root single uint64
chatim.v1.ForwardRef 3 seq single uint64
chatim.v1.ForwardRef 4 author single string
chatim.v1.ForwardRef 5 sent_at single message google.protobuf.Timestamp
chatim.v1.GetEditHistoryRequest 1 room_id single string
chatim.v1.GetEditHistoryRequest 2 thread_root single uint64
chatim.v1.GetEditHistoryRequest 3 seq single uint64
chatim.v1.GetEditHistoryRequest 4 after_ver single uint32
chatim.v1.GetEditHistoryRequest 5 limit single uint32
chatim.v1.GetHistoryRequest 1 room_id single string
chatim.v1.GetHistoryRequest 2 thread_root single uint64
chatim.v1.GetHistoryRequest 3 anchor single enum chatim.v1.HistoryAnchor
chatim.v1.GetHistoryRequest 4 seq single uint64
chatim.v1.GetHistoryRequest 5 limit single int32
chatim.v1.GetMessagesRequest 1 room_id single string
chatim.v1.GetMessagesRequest 2 thread_root single uint64
chatim.v1.GetMessagesRequest 3 seqs repeated uint64
chatim.v1.GetReactionSettingsRequest {}
chatim.v1.GetReactionsRequest 1 room_id single string
chatim.v1.GetReactionsRequest 2 thread_root single uint64
chatim.v1.GetReactionsRequest 3 seq single uint64
chatim.v1.GetReactionsRequest 4 emoji single string
chatim.v1.GetReactionsRequest 5 after single string
chatim.v1.GetReactionsRequest 6 limit single uint32
chatim.v1.GetRepliesRequest 1 room_id single string
chatim.v1.GetRepliesRequest 2 seq single uint64
chatim.v1.GetRepliesRequest 3 after single uint64
chatim.v1.GetRepliesRequest 4 limit single uint32
chatim.v1.GetRoomsRequest 1 room_ids repeated string
chatim.v1.HideMessageRequest 1 room_id single string
chatim.v1.HideMessageRequest 2 thread_root single uint64
chatim.v1.HideMessageRequest 3 seq single uint64
chatim.v1.HistoryAnchor = 0 HISTORY_ANCHOR_UNSPECIFIED
chatim.v1.HistoryAnchor = 1 HISTORY_ANCHOR_LATEST
chatim.v1.HistoryAnchor = 2 HISTORY_ANCHOR_OLDEST
chatim.v1.HistoryAnchor = 3 HISTORY_ANCHOR_BEFORE
chatim.v1.HistoryAnchor = 4 HISTORY_ANCHOR_AFTER
chatim.v1.HistoryAnchor = 5 HISTORY_ANCHOR_AROUND
chatim.v1.LeaveRoomRequest 1 room_id single string
chatim.v1.ListBookmarksRequest 1 before single string
chatim.v1.ListBookmarksRequest 2 limit single uint32
chatim.v1.ListMembersRequest 1 room_id single string
chatim.v1.ListMembersRequest 2 after single string
chatim.v1.ListMembersRequest 3 limit single uint32
chatim.v1.ListMentionsRequest 1 groups repeated message chatim.v1.MentionGroup
chatim.v1.ListMentionsRequest 2 before single string
chatim.v1.ListMentionsRequest 3 limit single uint32
chatim.v1.ListMyRoomsRequest 1 folder single enum chatim.v1.RoomFolder
chatim.v1.ListMyRoomsRequest 2 next single string
chatim.v1.ListMyRoomsRequest 3 limit single uint32
chatim.v1.ListPinsRequest 1 room_id single string
chatim.v1.ListRoomGroupsRequest 1 room_id single string
chatim.v1.ListThreadsRequest 1 room_id single string
chatim.v1.ListThreadsRequest 2 before single string
chatim.v1.ListThreadsRequest 3 limit single uint32
chatim.v1.MarkReadRequest 1 room_id single string
chatim.v1.MarkReadRequest 2 seq single uint64
chatim.v1.MarkUnreadRequest 1 room_id single string
chatim.v1.MarkUnreadRequest 2 seq single uint64
chatim.v1.MemberRole = 0 MEMBER_ROLE_UNSPECIFIED
chatim.v1.MemberRole = 1 MEMBER_ROLE_OWNER
chatim.v1.MemberRole = 2 MEMBER_ROLE_ADMIN
chatim.v1.MemberRole = 3 MEMBER_ROLE_MEMBER
chatim.v1.MentionGroup 1 id single string
chatim.v1.MentionGroup 2 since single message google.protobuf.Timestamp
chatim.v1.MentionKind = 0 MENTION_KIND_UNSPECIFIED
chatim.v1.MentionKind = 1 MENTION_KIND_USER
chatim.v1.MentionKind = 2 MENTION_KIND_GROUP
chatim.v1.MentionKind = 3 MENTION_KIND_ALL
chatim.v1.MentionSet 1 targets repeated message chatim.v1.MentionTarget
chatim.v1.MentionSet 2 all single bool
chatim.v1.MentionSet 3 all_at single message google.protobuf.Timestamp
chatim.v1.MentionTarget 1 kind single enum chatim.v1.MentionKind
chatim.v1.MentionTarget 2 id single string
chatim.v1.MentionTarget 3 at single message google.protobuf.Timestamp
chatim.v1.MessageKind = 0 MESSAGE_KIND_UNSPECIFIED
chatim.v1.MessageKind = 1 MESSAGE_KIND_TEXT
chatim.v1.MessageKind = 2 MESSAGE_KIND_SYSTEM
chatim.v1.OpenDirectRoomRequest 1 other_user single string
chatim.v1.PinMessageRequest 1 room_id single string
chatim.v1.PinMessageRequest 2 thread_root single uint64
chatim.v1.PinMessageRequest 3 seq single uint64
chatim.v1.ReactMessageRequest 1 room_id single string
chatim.v1.ReactMessageRequest 2 thread_root single uint64
chatim.v1.ReactMessageRequest 3 seq single uint64
chatim.v1.ReactMessageRequest 4 emoji single string
chatim.v1.RemoveMemberRequest 1 room_id single string
chatim.v1.RemoveMemberRequest 2 user single string
chatim.v1.ReplyRef 1 thread_root single uint64
chatim.v1.ReplyRef 2 seq single uint64
chatim.v1.RoomFolder = 0 ROOM_FOLDER_UNSPECIFIED
chatim.v1.RoomFolder = 1 ROOM_FOLDER_MAIN
chatim.v1.RoomFolder = 2 ROOM_FOLDER_ARCHIVED
chatim.v1.RoomType = 0 ROOM_TYPE_UNSPECIFIED
chatim.v1.RoomType = 1 ROOM_TYPE_DM
chatim.v1.RoomType = 2 ROOM_TYPE_GROUP
chatim.v1.SendMessageRequest 1 room_id single string
chatim.v1.SendMessageRequest 2 thread_root single uint64
chatim.v1.SendMessageRequest 3 cid single string
chatim.v1.SendMessageRequest 4 text single string
chatim.v1.SendMessageRequest 5 reply_to single message chatim.v1.ReplyRef
chatim.v1.SendMessageRequest 6 forward_from single message chatim.v1.ForwardRef
chatim.v1.SendMessageRequest 7 mentions single message chatim.v1.MentionSet
chatim.v1.SendMessageRequest 8 kind single enum chatim.v1.MessageKind
chatim.v1.SendMessageRequest 9 skip_unread single bool
chatim.v1.SendMessageRequest 10 meta single bytes
chatim.v1.SetBookmarkRequest 1 room_id single string
chatim.v1.SetBookmarkRequest 2 thread_root single uint64
chatim.v1.SetBookmarkRequest 3 seq single uint64
chatim.v1.SetBookmarkRequest 4 on single bool
chatim.v1.SetMemberPriorityRequest 1 room_id single string
chatim.v1.SetMemberPriorityRequest 2 user single string
chatim.v1.SetMemberPriorityRequest 3 priority single int32
chatim.v1.SetRoomPreferencesRequest 1 room_id single string
chatim.v1.SetRoomPreferencesRequest 2 pinned optional bool
chatim.v1.SetRoomPreferencesRequest 3 muted_until optional message google.protobuf.Timestamp
chatim.v1.SetRoomPreferencesRequest 4 archived optional bool
chatim.v1.UnpinMessageRequest 1 room_id single string
chatim.v1.UnpinMessageRequest 2 thread_root single uint64
chatim.v1.UnpinMessageRequest 3 seq single uint64
chatim.v1.UpdateRoomRequest 1 room_id single string
chatim.v1.UpdateRoomRequest 2 name optional string
chatim.v1.UpdateRoomRequest 3 avatar optional string
google.protobuf.Timestamp 1 seconds single int64
google.protobuf.Timestamp 2 nanos single int32
`

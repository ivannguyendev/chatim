package corecall

import (
	"slices"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var realtime = []string{
	chatimv1.CoreService_SendMessage_FullMethodName,
	chatimv1.CoreService_EditMessage_FullMethodName,
	chatimv1.CoreService_DeleteMessage_FullMethodName,
	chatimv1.CoreService_HideMessage_FullMethodName,
	chatimv1.CoreService_ReactMessage_FullMethodName,
	chatimv1.CoreService_PinMessage_FullMethodName,
	chatimv1.CoreService_UnpinMessage_FullMethodName,
	chatimv1.CoreService_SetBookmark_FullMethodName,
	chatimv1.CoreService_MarkRead_FullMethodName,
	chatimv1.CoreService_MarkUnread_FullMethodName,
	chatimv1.CoreService_ClearHistory_FullMethodName,
	chatimv1.CoreService_SetRoomPreferences_FullMethodName,
}

var request = []string{
	chatimv1.CoreService_ListMyRooms_FullMethodName,
	chatimv1.CoreService_GetRooms_FullMethodName,
	chatimv1.CoreService_GetHistory_FullMethodName,
	chatimv1.CoreService_GetMessages_FullMethodName,
	chatimv1.CoreService_GetEditHistory_FullMethodName,
	chatimv1.CoreService_GetReplies_FullMethodName,
	chatimv1.CoreService_ListThreads_FullMethodName,
	chatimv1.CoreService_ListPins_FullMethodName,
	chatimv1.CoreService_GetReactions_FullMethodName,
	chatimv1.CoreService_GetReactionSettings_FullMethodName,
	chatimv1.CoreService_ListBookmarks_FullMethodName,
	chatimv1.CoreService_ListMembers_FullMethodName,
	chatimv1.MentionService_ListMentions_FullMethodName,
	chatimv1.MentionService_ListRoomGroups_FullMethodName,
	chatimv1.CoreService_CreateRoom_FullMethodName,
	chatimv1.CoreService_OpenDirectRoom_FullMethodName,
	chatimv1.CoreService_UpdateRoom_FullMethodName,
	chatimv1.CoreService_AddMembers_FullMethodName,
	chatimv1.CoreService_RemoveMember_FullMethodName,
	chatimv1.CoreService_LeaveRoom_FullMethodName,
	chatimv1.CoreService_ChangeMemberRole_FullMethodName,
	chatimv1.CoreService_SetMemberPriority_FullMethodName,
}

func Realtime() []string { return slices.Clone(realtime) }

func Request() []string { return slices.Clone(request) }

func All() []string { return slices.Concat(realtime, request) }

package chatrpc

import (
	"slices"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var realtime = []string{
	chatimv1.ChatService_SendMessage_FullMethodName,
	chatimv1.ChatService_EditMessage_FullMethodName,
	chatimv1.ChatService_DeleteMessage_FullMethodName,
	chatimv1.ChatService_HideMessage_FullMethodName,
	chatimv1.ChatService_ReactMessage_FullMethodName,
	chatimv1.ChatService_PinMessage_FullMethodName,
	chatimv1.ChatService_UnpinMessage_FullMethodName,
	chatimv1.ChatService_SetBookmark_FullMethodName,
	chatimv1.ChatService_MarkRead_FullMethodName,
	chatimv1.ChatService_MarkUnread_FullMethodName,
	chatimv1.ChatService_ClearHistory_FullMethodName,
	chatimv1.ChatService_SetRoomPreferences_FullMethodName,
}

var request = []string{
	chatimv1.ChatService_ListMyRooms_FullMethodName,
	chatimv1.ChatService_GetRooms_FullMethodName,
	chatimv1.ChatService_GetHistory_FullMethodName,
	chatimv1.ChatService_GetMessages_FullMethodName,
	chatimv1.ChatService_GetEditHistory_FullMethodName,
	chatimv1.ChatService_GetReplies_FullMethodName,
	chatimv1.ChatService_ListThreads_FullMethodName,
	chatimv1.ChatService_ListPins_FullMethodName,
	chatimv1.ChatService_GetReactions_FullMethodName,
	chatimv1.ChatService_GetReactionSettings_FullMethodName,
	chatimv1.ChatService_ListBookmarks_FullMethodName,
	chatimv1.ChatService_ListMembers_FullMethodName,
	chatimv1.MentionService_ListMentions_FullMethodName,
	chatimv1.MentionService_ListRoomGroups_FullMethodName,
	chatimv1.ChatService_CreateRoom_FullMethodName,
	chatimv1.ChatService_OpenDirectRoom_FullMethodName,
	chatimv1.ChatService_UpdateRoom_FullMethodName,
	chatimv1.ChatService_AddMembers_FullMethodName,
	chatimv1.ChatService_RemoveMember_FullMethodName,
	chatimv1.ChatService_LeaveRoom_FullMethodName,
	chatimv1.ChatService_ChangeMemberRole_FullMethodName,
	chatimv1.ChatService_SetMemberPriority_FullMethodName,
	chatimv1.ChatService_GetClientConfig_FullMethodName,
	chatimv1.MediaService_StartUpload_FullMethodName,
	chatimv1.MediaService_GetPartURLs_FullMethodName,
	chatimv1.MediaService_CompleteUpload_FullMethodName,
	chatimv1.MediaService_AbortUpload_FullMethodName,
}

func Realtime() []string { return slices.Clone(realtime) }

func Request() []string { return slices.Clone(request) }

func All() []string { return slices.Concat(realtime, request) }

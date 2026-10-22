package wsclient

import chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"

const (
	reactionsCounter = "reactions"
	repliesCounter   = "replies"
)

type docKind uint8

const (
	docMember docKind = iota + 1
	docRead
	docPreferences
	docPins
	docRoomInfo
	docMemberCount
	docReactionCount
	docReplyCount
	docMessage
	docReaction
	docBookmark
)

type docKey struct {
	kind   docKind
	room   string
	user   string
	thread uint64
	seq    uint64
}

type docVersion struct {
	key        docKey
	ver        uint64
	recordOnly bool
}

func versionOf(ev *chatimv1.Event) (docVersion, bool) {
	room := ev.GetRoomId()
	message := docKey{room: room, thread: ev.GetThreadRoot(), seq: ev.GetSeq()}
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_MemberAdded:
		return userDoc(docMember, room, p.MemberAdded.GetUser(), uint64(p.MemberAdded.GetVer())), true
	case *chatimv1.Event_MemberRemoved:
		return userDoc(docMember, room, p.MemberRemoved.GetUser(), uint64(p.MemberRemoved.GetVer())), true
	case *chatimv1.Event_MemberRoleChanged:
		return userDoc(docMember, room, p.MemberRoleChanged.GetUser(), uint64(p.MemberRoleChanged.GetVer())), true
	case *chatimv1.Event_MemberPriorityChanged:
		return userDoc(docMember, room, p.MemberPriorityChanged.GetUser(), uint64(p.MemberPriorityChanged.GetVer())), true
	case *chatimv1.Event_RoomPreferencesChanged:
		return userDoc(docPreferences, room, p.RoomPreferencesChanged.GetUser(), p.RoomPreferencesChanged.GetState().GetPreferenceVer()), true
	case *chatimv1.Event_MessagePinned:
		return docVersion{key: docKey{kind: docPins, room: room}, ver: p.MessagePinned.GetPinVer()}, true
	case *chatimv1.Event_MessageUnpinned:
		return docVersion{key: docKey{kind: docPins, room: room}, ver: p.MessageUnpinned.GetPinVer()}, true
	case *chatimv1.Event_RoomUpdated:
		return docVersion{key: docKey{kind: docRoomInfo, room: room}, ver: p.RoomUpdated.GetRoom().GetInfoVer()}, true
	case *chatimv1.Event_MemberCountChanged:
		return docVersion{key: docKey{kind: docMemberCount, room: room}, ver: p.MemberCountChanged.GetMemberCountVer()}, true
	case *chatimv1.Event_CountsChanged:
		return countsVersion(message, p.CountsChanged)
	case *chatimv1.Event_MessageCreated:
		return messageDoc(message, uint64(p.MessageCreated.GetMessage().GetVer()), true), true
	case *chatimv1.Event_MessageEdited:
		return messageDoc(message, uint64(p.MessageEdited.GetVer()), false), true
	case *chatimv1.Event_MessageDeleted:
		return messageDoc(message, uint64(p.MessageDeleted.GetVer()), false), true
	case *chatimv1.Event_ReactionChanged:
		message.kind, message.user = docReaction, p.ReactionChanged.GetUser()
		return docVersion{key: message, ver: uint64(p.ReactionChanged.GetChange())}, true
	case *chatimv1.Event_BookmarkChanged:
		message.kind, message.user = docBookmark, p.BookmarkChanged.GetUser()
		return docVersion{key: message, ver: uint64(p.BookmarkChanged.GetVer())}, true
	default:
		return docVersion{}, false
	}
}

func userDoc(kind docKind, room, user string, ver uint64) docVersion {
	return docVersion{key: docKey{kind: kind, room: room, user: user}, ver: ver}
}

func messageDoc(key docKey, ver uint64, recordOnly bool) docVersion {
	key.kind = docMessage
	return docVersion{key: key, ver: ver, recordOnly: recordOnly}
}

func countsVersion(key docKey, counts *chatimv1.CountsChanged) (docVersion, bool) {
	switch counts.GetCounter() {
	case reactionsCounter:
		key.kind = docReactionCount
		return docVersion{key: key, ver: counts.GetReactions().GetVer()}, true
	case repliesCounter:
		key.kind = docReplyCount
		return docVersion{key: key, ver: counts.GetReplyCount().GetVer()}, true
	default:
		return docVersion{}, false
	}
}

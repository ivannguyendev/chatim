package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const updatedFieldsPath = "updateDescription.updatedFields."

var memberFeedFields = []string{"ver", "read_ver", "cleared_at"}

func decodeMemberChange(ev changeDoc) (store.Change, error) {
	if ev.OperationType == "update" {
		return memberFromUpdate(ev)
	}
	var d memberDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: member document: %w", errCorrupt, err)
	}
	m, err := decodeMember(d)
	if err != nil {
		return store.Change{}, err
	}
	if err := validMemberKey(m.Room, m.User); err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.MemberChanged, Member: m, CommittedAt: ev.WallTime}, nil
}

func memberFromUpdate(ev changeDoc) (store.Change, error) {
	_, id, ok := ev.DocumentKey.ID.BinaryOK()
	if !ok {
		return store.Change{}, fmt.Errorf("%w: member update without a binary _id", errCorrupt)
	}
	room, user, err := keys.ParseMember(id)
	if err != nil {
		return store.Change{}, fmt.Errorf("%w: member update _id: %w", errCorrupt, err)
	}
	if err := validMemberKey(room, user); err != nil {
		return store.Change{}, err
	}
	fields := ev.UpdateDescription.UpdatedFields
	c := store.Change{Member: domain.Member{Room: room, User: user}, CommittedAt: ev.WallTime}
	switch {
	case has(fields, "ver"):
		c.Kind = store.MemberChanged
		c.Member.Ver, err = positiveVer(fields, "ver")
	case has(fields, "read_ver"):
		var v uint32
		c.Kind = store.ReadChanged
		v, err = positiveVer(fields, "read_ver")
		c.Member.ReadVer = uint64(v)
	case has(fields, "cleared_at"):
		c.Kind = store.HistoryCleared
	default:
		err = fmt.Errorf("%w: member update without ver, read_ver or cleared_at", errCorrupt)
	}
	return c, err
}

func has(fields bson.Raw, key string) bool {
	_, err := fields.LookupErr(key)
	return err == nil
}

func positiveVer(fields bson.Raw, key string) (uint32, error) {
	raw, ok := fields.Lookup(key).AsInt64OK()
	if !ok {
		return 0, fmt.Errorf("%w: member update with a non-numeric %s", errCorrupt, key)
	}
	v, err := narrowUint32("member "+key, raw)
	if err != nil {
		return 0, err
	}
	if v == 0 {
		return 0, fmt.Errorf("%w: member update with %s 0", errCorrupt, key)
	}
	return v, nil
}

func validMemberKey(room uint64, user string) error {
	if room == 0 {
		return fmt.Errorf("%w: member of room 0", errCorrupt)
	}
	if err := domain.ValidUser(user); err != nil {
		return fmt.Errorf("%w: member user: %w", errCorrupt, err)
	}
	return nil
}

func decodeHiddenChange(ev changeDoc) (store.Change, error) {
	var d hiddenDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: hidden document: %w", errCorrupt, err)
	}
	h, err := decodeHidden(d)
	if err != nil {
		return store.Change{}, err
	}
	if err := (store.MsgKey{Room: h.Room, Thread: h.Thread, Seq: h.Seq}).Validate(); err != nil {
		return store.Change{}, fmt.Errorf("%w: hidden key: %w", errCorrupt, err)
	}
	if err := domain.ValidUser(h.User); err != nil {
		return store.Change{}, fmt.Errorf("%w: hidden user: %w", errCorrupt, err)
	}
	return store.Change{Kind: store.MessageHidden, Hidden: h, CommittedAt: ev.WallTime}, nil
}

func memberFeedBranches() bson.A {
	replaces := bson.D{{Key: "operationType", Value: "replace"}, {Key: "ns.coll", Value: membersCollection}}
	touched := bson.A{}
	for _, f := range memberFeedFields {
		touched = append(touched, bson.D{{Key: updatedFieldsPath + f, Value: bson.D{{Key: "$exists", Value: true}}}})
	}
	updates := bson.D{
		{Key: "operationType", Value: "update"},
		{Key: "ns.coll", Value: membersCollection},
		{Key: "$or", Value: touched},
	}
	return bson.A{replaces, updates}
}

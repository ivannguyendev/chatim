package mongostore

import (
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type editDoc struct {
	ID     []byte          `bson:"_id"`
	Room   int64           `bson:"room_id"`
	Tenant string          `bson:"tenant"`
	Kind   domain.EditKind `bson:"kind"`
	By     string          `bson:"created_by"`
	Text   string          `bson:"text,omitempty"`
	Prev   string          `bson:"p,omitempty"`
	At     time.Time       `bson:"created_at"`
}

func encodeEdit(e domain.Edit) (editDoc, error) {
	if err := store.ValidateEdit(e); err != nil {
		return editDoc{}, err
	}
	room, err := toInt64("room id", e.Room)
	if err != nil {
		return editDoc{}, err
	}
	if _, err := toInt64("seq", e.Seq); err != nil {
		return editDoc{}, err
	}
	return editDoc{
		ID:     keys.Edit(e.Room, e.Thread, e.Seq, e.Version),
		Room:   room,
		Tenant: e.Tenant,
		Kind:   e.Kind,
		By:     e.By,
		Text:   e.Text,
		Prev:   e.Prev,
		At:     e.At,
	}, nil
}

func decodeEdit(d editDoc) (domain.Edit, error) {
	room, thread, seq, version, err := keys.ParseEdit(d.ID)
	if err != nil {
		return domain.Edit{}, fmt.Errorf("%w: edit _id: %w", errCorrupt, err)
	}
	return domain.Edit{
		Room:    room,
		Thread:  thread,
		Seq:     seq,
		Version: version,
		Kind:    d.Kind,
		Tenant:  d.Tenant,
		By:      d.By,
		Text:    d.Text,
		Prev:    d.Prev,
		At:      d.At,
	}, nil
}

func decodeEdits(docs []editDoc) ([]domain.Edit, error) {
	out := make([]domain.Edit, len(docs))
	for i, d := range docs {
		e, err := decodeEdit(d)
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

func toInt32(field string, v uint32) (int32, error) {
	if v > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %s above max int32", apperr.ErrInvalidArgument, field)
	}
	return int32(v), nil
}

func toUint32(field string, v int32) (uint32, error) {
	if v < 0 {
		return 0, fmt.Errorf("%w: negative %s", errCorrupt, field)
	}
	return uint32(v), nil
}

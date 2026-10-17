package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	ErrPreviousPurged  = fmt.Errorf("previous message version purged by a later delete: %w", apperr.ErrUnavailable)
	errPreviousMissing = fmt.Errorf("previous message version has no edit row: %w", apperr.ErrUnavailable)
)

type EditVersions interface {
	At(ctx context.Context, key MsgKey, version uint32) (domain.Edit, error)
}

func PreviousMentions(ctx context.Context, edits EditVersions, snap domain.Message, known domain.Edit) (domain.Mentions, error) {
	if !snap.Deleted && snap.Version == 0 {
		return domain.Mentions{}, nil
	}
	want := snap.Version - 1
	if snap.Deleted {
		want = snap.Version
	}
	key := MsgKey{Room: snap.Room, Thread: snap.Thread, Seq: snap.Seq}
	row := known
	if row.Version != want || EditKeyOf(row) != key {
		got, err := edits.At(ctx, key, want)
		switch {
		case errors.Is(err, ErrEditNotFound):
			return domain.Mentions{}, fmt.Errorf("%w: v%d of %d/%d/%d", errPreviousMissing, want, key.Room, key.Thread, key.Seq)
		case err != nil:
			return domain.Mentions{}, err
		}
		row = got
	}
	if row.Kind != domain.EditDelete && row.Text == "" {
		return domain.Mentions{}, ErrPreviousPurged
	}
	return row.MentionSet(), nil
}

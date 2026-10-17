package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type editRows map[uint32]domain.Edit

func (r editRows) At(_ context.Context, key store.MsgKey, version uint32) (domain.Edit, error) {
	if e, ok := r[version]; ok && key == (store.MsgKey{Room: 1, Seq: 4}) {
		return e, nil
	}
	return domain.Edit{}, store.ErrEditNotFound
}

var factAt = time.UnixMilli(1_700_000_000_000).UTC()

func factWith(version uint32, kind domain.EditKind, text, target string) domain.Edit {
	return domain.Edit{
		Room: 1, Seq: 4, Version: version, Kind: kind, Text: text,
		Mentions: []domain.MentionTarget{{Kind: domain.MentionUser, ID: target, At: factAt}},
	}
}

func onlyTarget(t *testing.T, got domain.Mentions, err error, want string) {
	t.Helper()
	if err != nil || len(got.Targets) != 1 || got.Targets[0].ID != want || !got.Targets[0].At.Equal(factAt) {
		t.Fatalf("previous = %+v, %v; want only %s", got, err, want)
	}
}

func TestPreviousMentionsOfALiveSnapshotReadTheVersionBefore(t *testing.T) {
	rows := editRows{0: factWith(0, domain.EditOriginal, "v0", "an"), 1: factWith(1, domain.EditText, "v1", "binh")}
	snap := domain.Message{Room: 1, Seq: 4, Version: 2}
	got, err := store.PreviousMentions(context.Background(), rows, snap, factWith(1, domain.EditText, "v1", "binh"))
	onlyTarget(t, got, err, "binh")
	got, err = store.PreviousMentions(context.Background(), rows, domain.Message{Room: 1, Seq: 4, Version: 1}, factWith(1, domain.EditText, "v1", "binh"))
	onlyTarget(t, got, err, "an")
}

func TestPreviousMentionsOfADeletedSnapshotComeFromTheDeleteFact(t *testing.T) {
	del := factWith(3, domain.EditDelete, "", "chi")
	snap := domain.Message{Room: 1, Seq: 4, Version: 3, Deleted: true}
	got, err := store.PreviousMentions(context.Background(), editRows{3: del}, snap, factWith(1, domain.EditText, "v1", "binh"))
	onlyTarget(t, got, err, "chi")
	got, err = store.PreviousMentions(context.Background(), editRows{}, snap, del)
	onlyTarget(t, got, err, "chi")
}

func TestPreviousMentionsRefuseAPurgedVersion(t *testing.T) {
	rows := editRows{1: {Room: 1, Seq: 4, Version: 1, Kind: domain.EditText}}
	_, err := store.PreviousMentions(context.Background(), rows, domain.Message{Room: 1, Seq: 4, Version: 2}, domain.Edit{})
	if !errors.Is(err, store.ErrPreviousPurged) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("purged previous version: %v, want ErrPreviousPurged", err)
	}
}

func TestPreviousMentionsMissingRowIsNotGone(t *testing.T) {
	_, err := store.PreviousMentions(context.Background(), editRows{}, domain.Message{Room: 1, Seq: 4, Version: 2}, domain.Edit{})
	if errors.Is(err, store.ErrEditNotFound) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("missing previous row: %v, want unavailable and not edit-not-found", err)
	}
}

func TestPreviousMentionsOfAnUneditedSnapshotAreEmpty(t *testing.T) {
	got, err := store.PreviousMentions(context.Background(), editRows{}, domain.Message{Room: 1, Seq: 4}, domain.Edit{})
	if err != nil || got.Targets != nil || got.All {
		t.Fatalf("previous = %+v, %v; want empty", got, err)
	}
}

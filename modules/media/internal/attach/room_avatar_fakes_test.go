package attach_test

import (
	"context"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
)

const (
	group   = "9007199254740993"
	team    = "9007199254740995"
	avatarA = "acme/an/pub/20261113/0123456789abcdefghijklmnop"
	avatarB = "acme/an/pub/20261113/abcdefghijklmnop0123456789"
)

type countingAvatars struct {
	*memstore.RoomAvatars
	calls       map[string]int
	putErr      error
	beforeInUse func()
}

func (a *countingAvatars) Get(ctx context.Context, room string) (domain.RoomAvatar, bool, error) {
	a.calls["avatar_get"]++
	return a.RoomAvatars.Get(ctx, room)
}

func (a *countingAvatars) Put(ctx context.Context, next domain.RoomAvatar, prevVer uint64) error {
	a.calls["avatar_put"]++
	if err := a.putErr; err != nil {
		a.putErr = nil
		return err
	}
	return a.RoomAvatars.Put(ctx, next, prevVer)
}

func (a *countingAvatars) InUse(ctx context.Context, fileKey string) (bool, error) {
	a.calls["in_use"]++
	if hook := a.beforeInUse; hook != nil {
		a.beforeInUse = nil
		hook()
	}
	return a.RoomAvatars.InUse(ctx, fileKey)
}

func (r *rig) group(room string, ver uint64, keys ...string) {
	r.chat.setRoom(room, domain.RoomAvatarState{Found: true, Tenant: tenant, Group: true, Keys: keys, InfoVer: ver})
}

func (r *rig) roomUpdated(t *testing.T, room string) error {
	t.Helper()
	return r.svc.Sync(t.Context(), attach.RoomUpdated, attach.Event{Tenant: tenant, Ref: domain.MessageRef{Room: room}})
}

func (r *rig) settle(t *testing.T, room string) {
	t.Helper()
	if err := r.roomUpdated(t, room); err != nil {
		t.Fatalf("room_updated of %s: %v", room, err)
	}
}

func (r *rig) avatarOf(t *testing.T, room string) domain.RoomAvatar {
	t.Helper()
	doc, found, err := r.avatars.RoomAvatars.Get(t.Context(), room)
	if err != nil || !found {
		t.Fatalf("avatar doc of %s = %+v, %v, %v", room, doc, found, err)
	}
	return doc
}

func (r *rig) retired(t *testing.T, key string) bool {
	t.Helper()
	return !r.doc(t, key).RetiredAt.IsZero()
}

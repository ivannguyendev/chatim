package eventkit

import (
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func MessageKey(room, thread, seq uint64) store.MsgKey {
	return store.MsgKey{Room: room, Thread: thread, Seq: seq}
}

func Undeliverable(err error) bool {
	return errors.Is(err, domain.ErrRoomNotFound) || errors.Is(err, apperr.ErrInvalidArgument)
}

func Gone(err error) bool {
	return Undeliverable(err) || errors.Is(err, store.ErrEditNotFound) || errors.Is(err, domain.ErrMessageNotFound)
}

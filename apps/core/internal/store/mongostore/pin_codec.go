package mongostore

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type pinActionDoc struct {
	ID     []byte    `bson:"_id"`
	Room   int64     `bson:"r"`
	Tenant string    `bson:"t"`
	Op     int32     `bson:"op"`
	Thread int64     `bson:"th"`
	Seq    int64     `bson:"s"`
	By     string    `bson:"by"`
	At     time.Time `bson:"ts"`
}

type pinDoc struct {
	Thread int64     `bson:"th"`
	Seq    int64     `bson:"s"`
	By     string    `bson:"by"`
	At     time.Time `bson:"ts"`
	PV     int64     `bson:"pv"`
}

type pinStateDoc struct {
	Pins []pinDoc `bson:"pins"`
	PV   int64    `bson:"pv"`
}

func encodePinAction(a domain.PinAction) (pinActionDoc, error) {
	if err := store.ValidatePinAction(a); err != nil {
		return pinActionDoc{}, err
	}
	room, err := toInt64("room id", a.Room)
	if err != nil {
		return pinActionDoc{}, err
	}
	thread, err := toInt64("thread", a.Thread)
	if err != nil {
		return pinActionDoc{}, err
	}
	seq, err := toInt64("seq", a.Seq)
	if err != nil {
		return pinActionDoc{}, err
	}
	return pinActionDoc{
		ID: keys.Pin(a.Room, a.PV), Room: room, Tenant: a.Tenant, Op: int32(a.Op), Thread: thread, Seq: seq, By: a.By, At: a.At,
	}, nil
}

func decodePinAction(d pinActionDoc) (domain.PinAction, error) {
	room, pv, err := keys.ParsePin(d.ID)
	if err != nil {
		return domain.PinAction{}, fmt.Errorf("%w: pin _id: %w", errCorrupt, err)
	}
	op, err := decodePinOp(d.Op)
	if err != nil {
		return domain.PinAction{}, err
	}
	thread, err := toUint64("pin thread", d.Thread)
	if err != nil {
		return domain.PinAction{}, err
	}
	seq, err := toUint64("pin seq", d.Seq)
	if err != nil {
		return domain.PinAction{}, err
	}
	return domain.PinAction{Room: room, PV: pv, Tenant: d.Tenant, Op: op, Thread: thread, Seq: seq, By: d.By, At: d.At}, nil
}

func decodePinOp(op int32) (domain.PinOp, error) {
	switch op {
	case int32(domain.PinOpPin):
		return domain.PinOpPin, nil
	case int32(domain.PinOpUnpin):
		return domain.PinOpUnpin, nil
	default:
		return 0, fmt.Errorf("%w: pin op %d", errCorrupt, op)
	}
}

func decodePinActions(docs []pinActionDoc) ([]domain.PinAction, error) {
	out := make([]domain.PinAction, len(docs))
	for i, d := range docs {
		a, err := decodePinAction(d)
		if err != nil {
			return nil, err
		}
		out[i] = a
	}
	return out, nil
}

func encodePinState(s domain.PinState) ([]pinDoc, int64, error) {
	pv, err := toInt64("pin version", s.Version)
	if err != nil {
		return nil, 0, err
	}
	docs := make([]pinDoc, len(s.Pins))
	for i, p := range s.Pins {
		thread, threadErr := toInt64("pin thread", p.Thread)
		seq, seqErr := toInt64("pin seq", p.Seq)
		pinPV, pvErr := toInt64("pin version", p.PV)
		if threadErr != nil || seqErr != nil || pvErr != nil {
			return nil, 0, fmt.Errorf("encode pin %d of state v%d: %w", i, s.Version, firstErr(threadErr, seqErr, pvErr))
		}
		docs[i] = pinDoc{Thread: thread, Seq: seq, By: p.By, At: p.At, PV: pinPV}
	}
	return docs, pv, nil
}

func decodePinState(d pinStateDoc) (domain.PinState, error) {
	version, err := toUint64("pin version", d.PV)
	if err != nil {
		return domain.PinState{}, err
	}
	var pins []domain.Pin
	for _, p := range d.Pins {
		thread, threadErr := toUint64("pin thread", p.Thread)
		seq, seqErr := toUint64("pin seq", p.Seq)
		pv, pvErr := toUint64("pin version", p.PV)
		if err := firstErr(threadErr, seqErr, pvErr); err != nil {
			return domain.PinState{}, err
		}
		pins = append(pins, domain.Pin{Thread: thread, Seq: seq, By: p.By, At: p.At, PV: pv})
	}
	return domain.PinState{Pins: pins, Version: version}, nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

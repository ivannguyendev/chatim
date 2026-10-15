package memstore

import (
	"bytes"
	"cmp"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type flagState struct {
	bits store.StateBit
	at   time.Time
}

type flagMap[K comparable] map[K]flagState

func (m flagMap[K]) raise(k K, bit store.StateBit, at time.Time) {
	at = millis(at)
	cur, ok := m[k]
	if !ok || at.Before(cur.at) {
		cur.at = at
	}
	cur.bits |= bit
	m[k] = cur
}

func (m flagMap[K]) drop(k K, bit store.StateBit) {
	cur, ok := m[k]
	if !ok {
		return
	}
	cur.bits &^= bit
	if cur.bits == 0 {
		delete(m, k)
		return
	}
	m[k] = cur
}

func (m flagMap[K]) apply(k K, bit store.StateBit, mark bool, at time.Time) {
	if mark {
		m.raise(k, bit, at)
		return
	}
	m.drop(k, bit)
}

type pendingEntry struct {
	doc store.PendingDoc
	key []byte
}

func flagged[K comparable](m flagMap[K], doc func(K) (store.PendingDoc, []byte)) []pendingEntry {
	out := make([]pendingEntry, 0, len(m))
	for k, f := range m {
		d, key := doc(k)
		d.State, d.StateAt = f.bits, f.at
		out = append(out, pendingEntry{doc: d, key: key})
	}
	return out
}

func comparePending(a pendingEntry, at time.Time, key []byte) int {
	return cmp.Or(a.doc.StateAt.Compare(at), cmp.Compare(len(a.key), len(key)), bytes.Compare(a.key, key))
}

func millis(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return time.UnixMilli(t.UnixMilli()).UTC()
}

func sameMillis(a, b time.Time) bool {
	return millis(a).Equal(millis(b))
}

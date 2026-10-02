package slotmap

import (
	"strconv"
	"time"
)

const (
	ChangedChannel  = "chatim:slots:changed"
	CoreRegistryKey = "chatim:cores"

	corePrefix = "chatim:core:"
	slotPrefix = "chatim:slot:"
)

func CoreKey(id string) string { return corePrefix + id }

func SlotKey(slot uint16) string { return slotPrefix + strconv.Itoa(int(slot)) }

func CoreExpiryScore(at time.Time) float64 { return float64(at.UnixMilli()) }

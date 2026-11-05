package slotmap

import (
	"strconv"
	"time"
)

const (
	ChangedChannel  = "chatim:slots:changed"
	NodeRegistryKey = "chatim:chat-nodes"

	nodePrefix = "chatim:chat-node:"
	slotPrefix = "chatim:slot:"
)

func NodeKey(id string) string { return nodePrefix + id }

func SlotKey(slot uint16) string { return slotPrefix + strconv.Itoa(int(slot)) }

func NodeExpiryScore(at time.Time) float64 { return float64(at.UnixMilli()) }

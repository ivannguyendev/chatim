package slotmap

import (
	"strconv"
	"strings"
)

const (
	CorePattern    = corePrefix + "*"
	ChangedChannel = "chatim:slots:changed"

	corePrefix = "chatim:core:"
	slotPrefix = "chatim:slot:"
)

func CoreKey(id string) string { return corePrefix + id }

func CoreIDFromKey(key string) (string, bool) { return strings.CutPrefix(key, corePrefix) }

func SlotKey(slot uint16) string { return slotPrefix + strconv.Itoa(int(slot)) }

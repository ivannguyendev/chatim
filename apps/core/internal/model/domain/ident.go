package domain

import "github.com/ivannguyendev/chatim/pkg/ids"

func ValidTenant(s string) error { return ids.ValidTenant(s) }

func ValidUser(s string) error { return ids.ValidUser(s) }

func ValidCID(s string) error { return ids.ValidCID(s) }

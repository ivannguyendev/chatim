package store

import "time"

const MaxHiddenScan = 1000

func ValidateMarkTime(at time.Time) error {
	if at.IsZero() {
		return invalid("time")
	}
	return nil
}

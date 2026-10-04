package eventmark

import "time"

func SetClock(s *Store, now func() time.Time) { s.now = now }

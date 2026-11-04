package jsconsume

import "time"

var nakLadder = [...]time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

func NakDelay(numDelivered uint64) time.Duration {
	if numDelivered == 0 {
		numDelivered = 1
	}
	return nakLadder[min(numDelivered, uint64(len(nakLadder)))-1]
}

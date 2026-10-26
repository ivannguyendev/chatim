package auth

import "sync/atomic"

type Stats struct {
	LoginOK               atomic.Int64
	LoginInvalid          atomic.Int64
	LoginThrottledAccount atomic.Int64
	LoginThrottledIP      atomic.Int64
	LoginBusy             atomic.Int64
	LoginMustChange       atomic.Int64
	RefreshOK             atomic.Int64
	RefreshGrace          atomic.Int64
	RefreshReuse          atomic.Int64
	RefreshUnknown        atomic.Int64
	RefreshKeyMismatch    atomic.Int64
	RefreshExpired        atomic.Int64
	RefreshRevoked        atomic.Int64
	RefreshReplay         atomic.Int64
	RefreshUnavailable    atomic.Int64
	SessionsRevoked       atomic.Int64
	PasswordChanged       atomic.Int64
	StoreErrors           atomic.Int64
}

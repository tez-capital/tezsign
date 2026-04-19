package keychain

import "errors"

var (
	ErrKeyLocked            = errors.New("key locked")
	ErrKeyNotFound          = errors.New("key not found")
	ErrStaleWatermark       = errors.New("stale level/round")
	ErrBadPayload           = errors.New("bad sign payload")
	ErrUnsupportedOperation = errors.New("unsupported operation")
)

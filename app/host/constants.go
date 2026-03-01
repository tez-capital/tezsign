package main

import "time"

const (
	envDevice = "TEZSIGN_DEVICE"
	envKeys   = "TEZSIGN_UNLOCK_KEYS"
	envPass   = "TEZSIGN_UNLOCK_PASS"

	logFileName = "host.log"

	defaultPort = "20090"

	minKeepAlive = 10 * time.Millisecond
)

package main

const (
	rpcUnlockThrottled uint32 = 12

	rpcKeyNotFound    uint32 = 31
	rpcKeyLocked      uint32 = 32
	rpcStaleWatermark uint32 = 33
	rpcBadPayload     uint32 = 34

	rpcDeleteThrottled uint32 = 92
	rpcDeleteBadPass   uint32 = 93
)

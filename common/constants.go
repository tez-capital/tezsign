package common

const (
	VID = 0x9997
	PID = 0x0001

	VendorReqReady    = 0x5A
	bmReqTypeVendorIn = 0xC1 // IN | vendor | interface

	// error codes from rpc
	RpcKeyNotFound    uint32 = 31
	RpcKeyLocked      uint32 = 32
	RpcStaleWatermark uint32 = 33
	RpcBadPayload     uint32 = 34
)

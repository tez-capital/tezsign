package broker

const (
	_  = iota
	KB = 1 << (10 * iota) // 1 << 10 = 1024
	MB                    // 1 << 20 = 1048576
	GB                    // 1 << 30

	// DEFAULT_BROKER_CAPACITY is the max stash buffer before dropping oldest bytes.
	// It’s independent of frame pooling.
	DEFAULT_BROKER_CAPACITY = 50 * MB

	// MAX_MESSAGE_PAYLOAD is the largest allowed payload (excluding header).
	// By default: half of the broker capacity.
	MAX_MESSAGE_PAYLOAD = DEFAULT_BROKER_CAPACITY / 2

	// DEFAULT_READ_BUFFER is the size of the temporary read buffer per syscall.
	DEFAULT_READ_BUFFER = 256 * KB

	// MAX_POOLED_PAYLOAD is the largest payload (excluding header) that uses the pool.
	MAX_POOLED_PAYLOAD = 512 * KB
)

const (
	// MagicByte to know where from to start looking
	MagicByte = 0x56

	// Header fields: magic(1) + type(1) + id(16) + size(4) + parity(1)
	HeaderLen = 1 + 1 + 16 + 4 + 1
)

type payloadType byte

const (
	payloadTypeUnknown       payloadType = 0x00
	payloadTypeRequest       payloadType = 0x01
	payloadTypeResponse      payloadType = 0x02
	payloadTypeAcceptRequest payloadType = 0x03
	payloadTypeRetry         payloadType = 0x04
)

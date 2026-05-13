package secure

import (
	unsafe "unsafe"
)

//go:linkname memclrNoHeapPointers runtime.memclrNoHeapPointers
//go:noescape
func memclrNoHeapPointers(ptr unsafe.Pointer, len uintptr)

// MemoryWipe is the fastest, most secure way to zero a byte slice.
// It uses the Go runtime's internal, highly-optimized, non-optimizable
// memory clear function.
func MemoryWipe(b []byte) {
	if len(b) == 0 {
		return
	}

	memclrNoHeapPointers(unsafe.Pointer(&b[0]), uintptr(len(b)))
}

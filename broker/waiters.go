package broker

import (
	"maps"
	"sync"
)

// waiterMap is a tiny typed wrapper for sync.Map keyed by [16]byte.
type waiterMap struct {
	sync.Map
}

func (wm *waiterMap) NewWaiter() ([16]byte, chan []byte) {
	id := NewMessageID()
	ch := make(chan []byte, 1)
	wm.Map.Store(id, ch)
	return id, ch
}

func (wm *waiterMap) Delete(id [16]byte) {
	wm.Map.Delete(id)
}

func (wm *waiterMap) LoadAndDelete(id [16]byte) (chan []byte, bool) {
	v, ok := wm.Map.LoadAndDelete(id)
	if !ok {
		return nil, false
	}

	return v.(chan []byte), true
}

type requestMap[T any] struct {
	store map[[16]byte]T

	mtx sync.RWMutex
}

func NewRequestMap[T any]() requestMap[T] {
	return requestMap[T]{
		store: make(map[[16]byte]T),

		mtx: sync.RWMutex{},
	}
}

func (rm *requestMap[T]) Store(id [16]byte, payload T) {
	rm.mtx.Lock()
	defer rm.mtx.Unlock()
	rm.store[id] = payload
}

func (rm *requestMap[T]) HasRequest(id [16]byte) bool {
	rm.mtx.RLock()
	defer rm.mtx.RUnlock()
	_, ok := rm.store[id]
	return ok
}

func (rm *requestMap[T]) Delete(id [16]byte) {
	rm.mtx.Lock()
	defer rm.mtx.Unlock()
	delete(rm.store, id)
}

func (rm *requestMap[T]) All() map[[16]byte]T {
	rm.mtx.RLock()
	defer rm.mtx.RUnlock()
	return maps.Clone(rm.store)
}

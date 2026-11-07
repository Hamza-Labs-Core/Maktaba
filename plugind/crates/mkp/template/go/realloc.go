package main

import "unsafe"

// pinned keeps every block the host asked for alive until it frees it (newSize 0): the
// garbage collector cannot see the host's references into this memory.
var pinned = map[uintptr][]byte{}

// cabiRealloc is the canonical ABI's allocator, which the host calls to pass strings and
// lists in (TinyGo's wasm-unknown target has no runtime that exports one).
//
//go:wasmexport cabi_realloc
func cabiRealloc(ptr unsafe.Pointer, oldSize, align, newSize uint32) unsafe.Pointer {
	if newSize == 0 {
		delete(pinned, uintptr(ptr))
		return unsafe.Pointer(uintptr(align))
	}
	buf := make([]byte, newSize+align)
	p := (uintptr(unsafe.Pointer(&buf[0])) + uintptr(align) - 1) &^ (uintptr(align) - 1)
	if ptr != nil {
		n := oldSize
		if newSize < n {
			n = newSize
		}
		copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), n), unsafe.Slice((*byte)(ptr), n))
		delete(pinned, uintptr(ptr))
	}
	pinned[p] = buf
	return unsafe.Pointer(p)
}

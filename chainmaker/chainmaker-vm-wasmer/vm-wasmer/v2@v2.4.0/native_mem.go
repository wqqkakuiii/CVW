/*
Copyright (C) BABEC. All rights reserved.
SPDX-License-Identifier: Apache-2.0
*/

package wasmer

/*
#include <malloc.h>
#include <stdint.h>
#include <dlfcn.h>
#include <stdlib.h>
static uint64_t cm_native_uordblks(void) {
	struct mallinfo2 m = mallinfo2();
	return (uint64_t)m.uordblks;
}
static void cm_mark(const char *tag) {
	void (*fn)(const char *) = dlsym(RTLD_DEFAULT, "cm_malloc_mark");
	if (fn) fn(tag);
}
*/
import "C"
import "unsafe"

// nativeInUseBytes is glibc's current allocated-byte count (mallinfo2.uordblks).
// It sees Rust/malloc traffic that Go's HeapAlloc does not.
func nativeInUseBytes() uint64 {
	return uint64(C.cm_native_uordblks())
}

func mallocMark(tag string) {
	cs := C.CString(tag)
	defer C.free(unsafe.Pointer(cs))
	C.cm_mark(cs)
}

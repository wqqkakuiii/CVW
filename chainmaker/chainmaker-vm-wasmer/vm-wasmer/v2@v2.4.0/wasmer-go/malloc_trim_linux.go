//go:build linux && cgo

package wasmer

/*
#include <malloc.h>
*/
import "C"

// MallocTrim asks glibc to return free heap pages to the OS (malloc_trim(0)).
// Call after Wasmer Store/Instance teardown. Does not free Module/Engine JIT.
// No-op on non-Linux builds (see malloc_trim_stub.go).
func MallocTrim() {
	C.malloc_trim(0)
}

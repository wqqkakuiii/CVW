//go:build !linux || !cgo

package wasmer

// MallocTrim is a no-op when glibc malloc_trim is unavailable.
func MallocTrim() {}

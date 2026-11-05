//go:build !windows

package ort

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

func loadLib(path string) (uintptr, any, error) {
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return 0, nil, err
	}
	return h, nil, nil
}

func sym(h uintptr, name string) (uintptr, error) { return purego.Dlsym(h, name) }

// nativePath returns a NUL-terminated path for the C runtime.
func nativePath(s string) (unsafe.Pointer, any) {
	b := append([]byte(s), 0)
	return unsafe.Pointer(&b[0]), b
}

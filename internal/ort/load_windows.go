//go:build windows

package ort

import (
	"syscall"
	"unsafe"
)

func loadLib(path string) (uintptr, any, error) {
	d, err := syscall.LoadDLL(path)
	if err != nil {
		return 0, nil, err
	}
	return uintptr(unsafe.Pointer(d)), d, nil
}

func sym(h uintptr, name string) (uintptr, error) {
	d := (*syscall.DLL)(unsafePtr(h))
	p, err := d.FindProc(name)
	if err != nil {
		return 0, err
	}
	return p.Addr(), nil
}

// nativePath converts s to the ORTCHAR_T (UTF-16) representation ORT expects
// on Windows.
func nativePath(s string) (unsafe.Pointer, any) {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil, nil
	}
	return unsafe.Pointer(p), p
}

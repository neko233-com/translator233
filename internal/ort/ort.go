// Package ort is a minimal, cgo-free binding to the ONNX Runtime C API.
//
// The binding calls into libonnxruntime through its stable OrtApi function
// table, so the exact runtime version only needs to match the header the
// offset table in api_offsets.go was generated from (currently 1.31).
package ort

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// APIVersion is the ORT_API_VERSION the offset table was generated against.
const APIVersion = 31

// Logging levels (OrtLoggingLevel).
const (
	LogVerbose = 0
	LogInfo    = 1
	LogWarning = 2
	LogError   = 3
	LogFatal   = 4
)

// Tensor element types we use.
const (
	TensorFloat32 = 1
	TensorBool    = 9
	TensorInt64   = 7
)

// Error is a failed OrtStatus.
type Error struct {
	Code    int32
	Message string
}

func (e *Error) Error() string { return e.Message }

// Lib holds a loaded onnxruntime shared library.
type Lib struct {
	handle uintptr
	base   uintptr
	api    uintptr
	keep   any
}

var (
	global   *Lib
	globalMu sync.Mutex
)

// Load opens the onnxruntime shared library and resolves its API table.
func Load(path string) (*Lib, error) {
	h, keep, err := loadLib(path)
	if err != nil {
		return nil, fmt.Errorf("ort: load %s: %w", path, err)
	}
	getBase, err := sym(h, "OrtGetApiBase")
	if err != nil {
		return nil, fmt.Errorf("ort: OrtGetApiBase: %w", err)
	}
	base, _, _ := purego.SyscallN(getBase)
	if base == 0 {
		return nil, fmt.Errorf("ort: OrtGetApiBase returned nil")
	}
	getAPI := deref(base) // OrtApiBase member 0
	api, _, _ := purego.SyscallN(getAPI, APIVersion)
	if api == 0 {
		return nil, fmt.Errorf("ort: OrtGetApi does not support api version %d", APIVersion)
	}
	return &Lib{handle: h, base: base, api: api, keep: keep}, nil
}

// Version reports the runtime version string.
func (l *Lib) Version() string {
	if l.base == 0 {
		return ""
	}
	r1, _, _ := purego.SyscallN(deref(l.base + unsafe.Sizeof(uintptr(0))))
	return cstr(r1)
}

// SetShared installs the process-wide runtime used by the package helpers.
func SetShared(l *Lib) {
	globalMu.Lock()
	defer globalMu.Unlock()
	global = l
}

func shared() (*Lib, error) {
	globalMu.Lock()
	defer globalMu.Unlock()
	if global == nil {
		return nil, fmt.Errorf("ort: runtime not initialized")
	}
	return global, nil
}

func deref(p uintptr) uintptr {
	return *(*uintptr)(unsafePtr(p))
}

// unsafePtr reinterprets a native address as a pointer. Addresses coming back
// from the C API are stable for as long as the owning OrtValue/session lives,
// because Go's collector does not move heap objects.
func unsafePtr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

func (l *Lib) fn(name string) uintptr {
	idx, ok := apiIndex[name]
	if !ok {
		panic("ort: unknown api function " + name)
	}
	return deref(l.api + uintptr(idx)*unsafe.Sizeof(uintptr(0)))
}

// call invokes an OrtApi member.
//
// //go:uintptrescapes is required: out-parameters are passed as
// uintptr(unsafe.Pointer(&local)), and the goroutine's stack can move while
// the foreign call runs. Without the pragma the referent stays on the stack,
// the library writes into the old copy, and the caller reads back zero — which
// on macOS arm64 produced "CreateSession succeeded with a NULL session".
//
//go:uintptrescapes
func (l *Lib) call(name string, args ...uintptr) (uintptr, error) {
	r1, _, _ := purego.SyscallN(l.fn(name), args...)
	return r1, nil
}

// status interprets r1 as an OrtStatus pointer.
func (l *Lib) status(r1 uintptr) error {
	if r1 == 0 {
		return nil
	}
	msgPtr, _, _ := purego.SyscallN(l.fn("GetErrorMessage"), r1)
	code, _, _ := purego.SyscallN(l.fn("GetErrorCode"), r1)
	msg := cstr(msgPtr)
	purego.SyscallN(l.fn("ReleaseStatus"), r1)
	return &Error{Code: int32(code), Message: msg}
}

func cstr(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for off := uintptr(0); off < 1<<16; off++ {
		c := *(*byte)(unsafe.Add(unsafePtr(p), off))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

// cbytes returns a NUL-terminated copy of s that outlives the call.
func cbytes(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// Env is an OrtEnv.
type Env struct{ ptr uintptr }

// NewEnv creates the process environment.
func (l *Lib) NewEnv(level int, name string) (*Env, error) {
	id := cbytes(name)
	var env uintptr
	r1, _ := l.call("CreateEnv", uintptr(level), uintptr(unsafe.Pointer(&id[0])), uintptr(unsafe.Pointer(&env)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	return &Env{ptr: env}, nil
}

// Release frees the environment.
func (e *Env) Release(l *Lib) {
	if e.ptr != 0 {
		purego.SyscallN(l.fn("ReleaseEnv"), e.ptr)
		e.ptr = 0
	}
}

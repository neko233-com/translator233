package ort

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// SessionOptions configures a Session.
type SessionOptions struct{ ptr uintptr }

// NewSessionOptions creates default options tuned for low-latency CPU inference.
func (l *Lib) NewSessionOptions(threads int) (*SessionOptions, error) {
	var opts uintptr
	r1, _ := l.call("CreateSessionOptions", uintptr(unsafe.Pointer(&opts)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	o := &SessionOptions{ptr: opts}
	if threads > 0 {
		if err := l.check("SetIntraOpNumThreads", opts, uintptr(threads)); err != nil {
			return nil, err
		}
	}
	if err := l.check("SetSessionGraphOptimizationLevel", opts, 99); err != nil {
		return nil, err
	}
	if err := l.check("SetSessionLogSeverityLevel", opts, LogError); err != nil {
		return nil, err
	}
	return o, nil
}

// Release frees the options.
func (o *SessionOptions) Release(l *Lib) {
	if o.ptr != 0 {
		purego.SyscallN(l.fn("ReleaseSessionOptions"), o.ptr)
		o.ptr = 0
	}
}

func (l *Lib) check(name string, args ...uintptr) error {
	r1, _, _ := purego.SyscallN(l.fn(name), args...)
	return l.status(r1)
}

// Session is a loaded ONNX graph.
type Session struct {
	l       *Lib
	ptr     uintptr
	memInfo uintptr

	// Inputs and Outputs list the graph's tensor names in model order.
	Inputs  []string
	Outputs []string
}

// NewSession loads modelPath.
func (l *Lib) NewSession(env *Env, modelPath string, opts *SessionOptions) (*Session, error) {
	name, keepName := nativePath(modelPath)
	var sess uintptr
	r1, _ := l.call("CreateSession", env.ptr, uintptr(name), opts.ptr, uintptr(unsafe.Pointer(&sess)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	s := &Session{l: l, ptr: sess}

	var mem uintptr
	r1, _ = l.call("CreateCpuMemoryInfo", 1 /* OrtArenaAllocator */, 0 /* OrtMemTypeDefault */, uintptr(unsafe.Pointer(&mem)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	s.memInfo = mem

	inputs, err := l.ioNames(sess, true)
	if err != nil {
		return nil, err
	}
	outputs, err := l.ioNames(sess, false)
	if err != nil {
		return nil, err
	}
	s.Inputs, s.Outputs = inputs, outputs
	runtime.KeepAlive(keepName)
	return s, nil
}

func (l *Lib) ioNames(sess uintptr, input bool) ([]string, error) {
	countFn, nameFn := "SessionGetOutputCount", "SessionGetOutputName"
	if input {
		countFn, nameFn = "SessionGetInputCount", "SessionGetInputName"
	}
	var n uintptr
	r1, _ := l.call(countFn, sess, uintptr(unsafe.Pointer(&n)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	var alloc uintptr
	r1, _ = l.call("GetAllocatorWithDefaultOptions", uintptr(unsafe.Pointer(&alloc)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		var p uintptr
		r1, _ = l.call(nameFn, sess, i, alloc, uintptr(unsafe.Pointer(&p)))
		if err := l.status(r1); err != nil {
			return nil, err
		}
		out = append(out, cstr(p))
		purego.SyscallN(l.fn("AllocatorFree"), alloc, p)
	}
	return out, nil
}

// Close releases the session.
func (s *Session) Close() {
	if s.ptr != 0 {
		purego.SyscallN(s.l.fn("ReleaseSession"), s.ptr)
		s.ptr = 0
	}
	if s.memInfo != 0 {
		purego.SyscallN(s.l.fn("ReleaseMemoryInfo"), s.memInfo)
		s.memInfo = 0
	}
}

// Tensor wraps an OrtValue.
type Tensor struct {
	ptr  uintptr
	keep any // keeps the backing Go memory alive while ORT reads it
	elem int
}

// NewTensor creates an OrtValue sharing data. The caller must keep data alive
// for the duration of the Run call.
func (l *Lib) NewTensor(sess *Session, elem int, shape []int64, data unsafe.Pointer, nbytes int) (*Tensor, error) {
	var v uintptr
	r1, _ := l.call("CreateTensorWithDataAsOrtValue", sess.memInfo, uintptr(data), uintptr(nbytes),
		uintptr(unsafe.Pointer(&shape[0])), uintptr(len(shape)), uintptr(elem), uintptr(unsafe.Pointer(&v)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	return &Tensor{ptr: v, elem: elem}, nil
}

// NewEmptyTensor allocates an OrtValue of the given shape and element type.
func (l *Lib) NewEmptyTensor(allocSession *Session, elem int, shape []int64) (*Tensor, error) {
	var v uintptr
	r1, _ := l.call("CreateTensorAsOrtValue", allocSession.memInfo,
		uintptr(unsafe.Pointer(&shape[0])), uintptr(len(shape)), uintptr(elem), uintptr(unsafe.Pointer(&v)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	return &Tensor{ptr: v, elem: elem}, nil
}

// Close releases the tensor.
func (t *Tensor) Close(l *Lib) {
	if t.ptr != 0 {
		purego.SyscallN(l.fn("ReleaseValue"), t.ptr)
		t.ptr = 0
	}
	t.keep = nil
}

// Shape returns the tensor dimensions.
func (t *Tensor) Shape(l *Lib) ([]int64, error) {
	var info uintptr
	r1, _ := l.call("GetTensorTypeAndShape", t.ptr, uintptr(unsafe.Pointer(&info)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	defer purego.SyscallN(l.fn("ReleaseTensorTypeAndShapeInfo"), info)
	var n uintptr
	r1, _ = l.call("GetDimensionsCount", info, uintptr(unsafe.Pointer(&n)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	dims := make([]int64, n)
	if n == 0 {
		return dims, nil
	}
	r1, _ = l.call("GetDimensions", info, uintptr(unsafe.Pointer(&dims[0])), n)
	if err := l.status(r1); err != nil {
		return nil, err
	}
	return dims, nil
}

// Float32 returns the tensor data as a float32 slice (no copy).
func (t *Tensor) Float32(l *Lib) ([]float32, error) {
	var p uintptr
	r1, _ := l.call("GetTensorMutableData", t.ptr, uintptr(unsafe.Pointer(&p)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	var count uintptr
	var info uintptr
	r1, _ = l.call("GetTensorTypeAndShape", t.ptr, uintptr(unsafe.Pointer(&info)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	defer purego.SyscallN(l.fn("ReleaseTensorTypeAndShapeInfo"), info)
	r1, _ = l.call("GetTensorShapeElementCount", info, uintptr(unsafe.Pointer(&count)))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	return unsafe.Slice((*float32)(unsafePtr(p)), count), nil
}

// Run executes the session with one value per input, in Session.Inputs order.
func (s *Session) Run(inputs []*Tensor) ([]*Tensor, error) {
	if len(inputs) != len(s.Inputs) {
		return nil, fmt.Errorf("ort: got %d inputs, model wants %d", len(inputs), len(s.Inputs))
	}
	l := s.l
	inNames := make([]uintptr, len(s.Inputs))
	inVals := make([]uintptr, len(inputs))
	inRefs := make([][]byte, len(s.Inputs))
	for i, n := range s.Inputs {
		inRefs[i] = cbytes(n)
		inNames[i] = uintptr(unsafe.Pointer(&inRefs[i][0]))
		inVals[i] = inputs[i].ptr
	}
	outNames := make([]uintptr, len(s.Outputs))
	outRefs := make([][]byte, len(s.Outputs))
	outVals := make([]uintptr, len(s.Outputs))
	for i, n := range s.Outputs {
		outRefs[i] = cbytes(n)
		outNames[i] = uintptr(unsafe.Pointer(&outRefs[i][0]))
	}
	r1, _ := l.call("Run", s.ptr, 0,
		uintptr(unsafe.Pointer(&inNames[0])), uintptr(unsafe.Pointer(&inVals[0])), uintptr(len(inNames)),
		uintptr(unsafe.Pointer(&outNames[0])), uintptr(len(outNames)), uintptr(unsafe.Pointer(&outVals[0])))
	if err := l.status(r1); err != nil {
		return nil, err
	}
	out := make([]*Tensor, len(outVals))
	for i, v := range outVals {
		out[i] = &Tensor{ptr: v}
	}
	runtime.KeepAlive(inRefs)
	runtime.KeepAlive(outRefs)
	runtime.KeepAlive(inputs)
	return out, nil
}

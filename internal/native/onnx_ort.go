package native

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Cgo-free purego binding to the ONNX Runtime C API: OrtApi is a struct of function pointers in stable order, read by index for the pinned version.
const ortAPIVersion = 27

const (
	onnxTypeFloat = 1
	onnxTypeInt64 = 7
)

type ortAPI struct {
	getErrorMessage               func(uintptr) uintptr
	createEnv                     func(int32, string, unsafe.Pointer) uintptr
	createSessionOptions          func(unsafe.Pointer) uintptr
	setIntraOpNumThreads          func(uintptr, int32) uintptr
	createSession                 func(uintptr, string, uintptr, unsafe.Pointer) uintptr
	getAllocatorWithDefaultOpts   func(unsafe.Pointer) uintptr
	sessionGetInputCount          func(uintptr, unsafe.Pointer) uintptr
	sessionGetOutputCount         func(uintptr, unsafe.Pointer) uintptr
	sessionGetInputName           func(uintptr, uint64, uintptr, unsafe.Pointer) uintptr
	sessionGetOutputName          func(uintptr, uint64, uintptr, unsafe.Pointer) uintptr
	createCpuMemoryInfo           func(int32, int32, unsafe.Pointer) uintptr
	createTensorWithData          func(uintptr, unsafe.Pointer, uint64, unsafe.Pointer, uint64, int32, unsafe.Pointer) uintptr
	run                           func(uintptr, uintptr, unsafe.Pointer, unsafe.Pointer, uint64, unsafe.Pointer, uint64, unsafe.Pointer) uintptr
	getTensorMutableData          func(uintptr, unsafe.Pointer) uintptr
	getTensorTypeAndShape         func(uintptr, unsafe.Pointer) uintptr
	getDimensionsCount            func(uintptr, unsafe.Pointer) uintptr
	getDimensions                 func(uintptr, unsafe.Pointer, uint64) uintptr
	getTensorShapeElementCount    func(uintptr, unsafe.Pointer) uintptr
	allocatorFree                 func(uintptr, uintptr) uintptr
	releaseEnv                    func(uintptr)
	releaseStatus                 func(uintptr)
	releaseSessionOptions         func(uintptr)
	releaseSession                func(uintptr)
	releaseValue                  func(uintptr)
	releaseMemoryInfo             func(uintptr)
	releaseTensorTypeAndShapeInfo func(uintptr)
}

var (
	ortMu   sync.Mutex
	ortByLib = map[string]*ortAPI{}
)

func ortReadPtr(base uintptr, index int) uintptr {
	return *(*uintptr)(unsafe.Pointer(base + uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

func getORT(libPath string) (*ortAPI, error) {
	ortMu.Lock()
	defer ortMu.Unlock()
	if a, ok := ortByLib[libPath]; ok {
		return a, nil
	}
	a, err := loadORT(libPath)
	if err != nil {
		return nil, err
	}
	ortByLib[libPath] = a
	return a, nil
}

func loadORT(libPath string) (*ortAPI, error) {
	handle, err := purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("onnx: cannot load ONNX Runtime at %q: %w", libPath, err)
	}
	addr, err := purego.Dlsym(handle, "OrtGetApiBase")
	if err != nil {
		return nil, fmt.Errorf("onnx: OrtGetApiBase not found in %q: %w", libPath, err)
	}
	var getApiBase func() uintptr
	purego.RegisterFunc(&getApiBase, addr)
	base := getApiBase()
	if base == 0 {
		return nil, fmt.Errorf("onnx: OrtGetApiBase returned null")
	}
	var getApi func(uint32) uintptr
	purego.RegisterFunc(&getApi, ortReadPtr(base, 0))
	api := getApi(ortAPIVersion)
	if api == 0 {
		return nil, fmt.Errorf("onnx: runtime does not support ORT API version %d (use a newer ONNX Runtime)", ortAPIVersion)
	}
	a := &ortAPI{}
	reg := func(fn any, index int) { purego.RegisterFunc(fn, ortReadPtr(api, index)) }
	reg(&a.getErrorMessage, 2)
	reg(&a.createEnv, 3)
	reg(&a.createSession, 7)
	reg(&a.run, 9)
	reg(&a.createSessionOptions, 10)
	reg(&a.setIntraOpNumThreads, 24)
	reg(&a.sessionGetInputCount, 30)
	reg(&a.sessionGetOutputCount, 31)
	reg(&a.sessionGetInputName, 36)
	reg(&a.sessionGetOutputName, 37)
	reg(&a.createTensorWithData, 49)
	reg(&a.getTensorMutableData, 51)
	reg(&a.getDimensionsCount, 61)
	reg(&a.getDimensions, 62)
	reg(&a.getTensorShapeElementCount, 64)
	reg(&a.getTensorTypeAndShape, 65)
	reg(&a.createCpuMemoryInfo, 69)
	reg(&a.allocatorFree, 76)
	reg(&a.getAllocatorWithDefaultOpts, 78)
	reg(&a.releaseEnv, 92)
	reg(&a.releaseStatus, 93)
	reg(&a.releaseMemoryInfo, 94)
	reg(&a.releaseSession, 95)
	reg(&a.releaseValue, 96)
	reg(&a.releaseTensorTypeAndShapeInfo, 99)
	reg(&a.releaseSessionOptions, 100)
	return a, nil
}

func (a *ortAPI) check(status uintptr, op string) error {
	if status == 0 {
		return nil
	}
	msg := ortCString(a.getErrorMessage(status))
	a.releaseStatus(status)
	return fmt.Errorf("onnx: %s: %s", op, msg)
}

func ortCString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Pointer(p + uintptr(n))) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(unsafe.Pointer(p)), n))
}

// ONNXSession is a loaded model. Not safe for concurrent Run on one instance.
type ONNXSession struct {
	api         *ortAPI
	env         uintptr
	options     uintptr
	session     uintptr
	memInfo     uintptr
	allocator   uintptr
	inputNames  []string
	outputNames []string
	namePtrs    []uintptr
	closed      bool
}

// ONNXInput is one input tensor (int64 only; BERT encoders take int64 ids/masks).
type ONNXInput struct {
	data  []int64
	shape []int64
}

// ONNXOutput is one float32 output tensor with its shape.
type ONNXOutput struct {
	data  []float32
	shape []int64
}

func NewONNXInput(data, shape []int64) ONNXInput { return ONNXInput{data: data, shape: shape} }
func (o ONNXOutput) Data() []float32             { return o.data }
func (o ONNXOutput) Shape() []int64              { return o.shape }
func (s *ONNXSession) InputNames() []string      { return s.inputNames }
func (s *ONNXSession) OutputNames() []string     { return s.outputNames }

func NewONNXSession(libPath, modelPath string, intraThreads int) (*ONNXSession, error) {
	api, err := getORT(libPath)
	if err != nil {
		return nil, err
	}
	s := &ONNXSession{api: api}
	if err := api.check(api.createEnv(2, "geblang", unsafe.Pointer(&s.env)), "create env"); err != nil {
		return nil, err
	}
	if err := api.check(api.createSessionOptions(unsafe.Pointer(&s.options)), "create session options"); err != nil {
		s.Close()
		return nil, err
	}
	if intraThreads > 0 {
		api.setIntraOpNumThreads(s.options, int32(intraThreads))
	}
	if err := api.check(api.createSession(s.env, modelPath, s.options, unsafe.Pointer(&s.session)), "create session"); err != nil {
		s.Close()
		return nil, err
	}
	if err := api.check(api.getAllocatorWithDefaultOpts(unsafe.Pointer(&s.allocator)), "get allocator"); err != nil {
		s.Close()
		return nil, err
	}
	if err := api.check(api.createCpuMemoryInfo(1, 0, unsafe.Pointer(&s.memInfo)), "create memory info"); err != nil {
		s.Close()
		return nil, err
	}
	var inCount, outCount uint64
	if err := api.check(api.sessionGetInputCount(s.session, unsafe.Pointer(&inCount)), "input count"); err != nil {
		s.Close()
		return nil, err
	}
	if err := api.check(api.sessionGetOutputCount(s.session, unsafe.Pointer(&outCount)), "output count"); err != nil {
		s.Close()
		return nil, err
	}
	for i := uint64(0); i < inCount; i++ {
		var name uintptr
		if err := api.check(api.sessionGetInputName(s.session, i, s.allocator, unsafe.Pointer(&name)), "input name"); err != nil {
			s.Close()
			return nil, err
		}
		s.inputNames = append(s.inputNames, ortCString(name))
		s.namePtrs = append(s.namePtrs, name)
	}
	for i := uint64(0); i < outCount; i++ {
		var name uintptr
		if err := api.check(api.sessionGetOutputName(s.session, i, s.allocator, unsafe.Pointer(&name)), "output name"); err != nil {
			s.Close()
			return nil, err
		}
		s.outputNames = append(s.outputNames, ortCString(name))
		s.namePtrs = append(s.namePtrs, name)
	}
	return s, nil
}

// run feeds the named int64 inputs and returns the named float32 outputs.
func (s *ONNXSession) Run(inputs map[string]ONNXInput, outputNames []string) (map[string]ONNXOutput, error) {
	api := s.api
	var pin runtime.Pinner
	defer pin.Unpin()

	names := make([]string, 0, len(inputs))
	for n := range inputs {
		names = append(names, n)
	}
	inNamePtrs := make([]uintptr, len(names))
	inValues := make([]uintptr, len(names))
	defer func() {
		for _, v := range inValues {
			if v != 0 {
				api.releaseValue(v)
			}
		}
	}()
	for i, n := range names {
		in := inputs[n]
		cname := append([]byte(n), 0)
		pin.Pin(&cname[0])
		inNamePtrs[i] = uintptr(unsafe.Pointer(&cname[0]))
		if len(in.data) == 0 {
			return nil, fmt.Errorf("onnx: input %q is empty", n)
		}
		pin.Pin(&in.data[0])
		pin.Pin(&in.shape[0])
		var val uintptr
		st := api.createTensorWithData(s.memInfo, unsafe.Pointer(&in.data[0]), uint64(len(in.data)*8),
			unsafe.Pointer(&in.shape[0]), uint64(len(in.shape)), onnxTypeInt64, unsafe.Pointer(&val))
		if err := api.check(st, "create input tensor "+n); err != nil {
			return nil, err
		}
		inValues[i] = val
	}

	outNamePtrs := make([]uintptr, len(outputNames))
	for i, n := range outputNames {
		cname := append([]byte(n), 0)
		pin.Pin(&cname[0])
		outNamePtrs[i] = uintptr(unsafe.Pointer(&cname[0]))
	}
	outValues := make([]uintptr, len(outputNames))
	pin.Pin(&inNamePtrs[0])
	pin.Pin(&inValues[0])
	pin.Pin(&outNamePtrs[0])
	pin.Pin(&outValues[0])
	defer func() {
		for _, v := range outValues {
			if v != 0 {
				api.releaseValue(v)
			}
		}
	}()

	st := api.run(s.session, 0, unsafe.Pointer(&inNamePtrs[0]), unsafe.Pointer(&inValues[0]), uint64(len(names)),
		unsafe.Pointer(&outNamePtrs[0]), uint64(len(outputNames)), unsafe.Pointer(&outValues[0]))
	if err := api.check(st, "run"); err != nil {
		return nil, err
	}

	out := make(map[string]ONNXOutput, len(outputNames))
	for i, n := range outputNames {
		shape, count, err := s.tensorShape(outValues[i])
		if err != nil {
			return nil, err
		}
		var dataPtr uintptr
		if err := api.check(api.getTensorMutableData(outValues[i], unsafe.Pointer(&dataPtr)), "get output data"); err != nil {
			return nil, err
		}
		floats := make([]float32, count)
		copy(floats, unsafe.Slice((*float32)(unsafe.Pointer(dataPtr)), count))
		out[n] = ONNXOutput{data: floats, shape: shape}
	}
	return out, nil
}

func (s *ONNXSession) tensorShape(value uintptr) ([]int64, int, error) {
	api := s.api
	var info uintptr
	if err := api.check(api.getTensorTypeAndShape(value, unsafe.Pointer(&info)), "tensor type/shape"); err != nil {
		return nil, 0, err
	}
	defer api.releaseTensorTypeAndShapeInfo(info)
	var ndim, count uint64
	if err := api.check(api.getDimensionsCount(info, unsafe.Pointer(&ndim)), "dim count"); err != nil {
		return nil, 0, err
	}
	dims := make([]int64, ndim)
	if ndim > 0 {
		if err := api.check(api.getDimensions(info, unsafe.Pointer(&dims[0]), ndim), "dims"); err != nil {
			return nil, 0, err
		}
	}
	if err := api.check(api.getTensorShapeElementCount(info, unsafe.Pointer(&count)), "element count"); err != nil {
		return nil, 0, err
	}
	return dims, int(count), nil
}

func (s *ONNXSession) Close() {
	if s.closed {
		return
	}
	s.closed = true
	api := s.api
	for _, p := range s.namePtrs {
		if p != 0 && s.allocator != 0 {
			api.allocatorFree(s.allocator, p)
		}
	}
	if s.session != 0 {
		api.releaseSession(s.session)
	}
	if s.memInfo != 0 {
		api.releaseMemoryInfo(s.memInfo)
	}
	if s.options != 0 {
		api.releaseSessionOptions(s.options)
	}
	if s.env != 0 {
		api.releaseEnv(s.env)
	}
}

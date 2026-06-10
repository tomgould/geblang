package bytecode

import (
	"strings"
	"sync"

	"geblang/internal/runtime"
)

// chunkSharedMeta is created once per compiled/decoded chunk and shared
// by every VM bound to a copy of that chunk: the prepared (memoised)
// function metadata, the class index, the type-assert specs, and the
// wrapper templates. All fields are written exactly once (under `once`
// or `mu`) and read-only afterwards, so concurrent VMs share them
// without copying or re-deriving per construction.
type chunkSharedMeta struct {
	once                         sync.Once
	preparedFunctions            []FunctionInfo
	classIndex                   map[string]int
	typeAssertSpecs              map[int64]vmTypeSpec
	requiresCallSitePolymorphism bool

	mu       sync.Mutex
	wrappers map[wrapperKey]*wrapperTemplate
}

func newChunkSharedMeta() *chunkSharedMeta { return &chunkSharedMeta{} }

// wrapperKey identifies a wrapper-derived chunk shape: every shifted
// slice in the derivation depends only on the wrapper length and the
// number of prepended argument constants.
type wrapperKey struct {
	shift      int
	constShift int
}

// wrapperTemplate is the shareable, fully shifted part of a
// wrapper-derived chunk. Constants stay per-call (they carry the call
// arguments and the parent's current constant pool, which static
// assignments mutate at runtime).
type wrapperTemplate struct {
	functions []FunctionInfo
	classes   []ClassInfo
	tail      []Instruction
	meta      *chunkSharedMeta
	// vmPool recycles wrapper VMs whose run handed out no vm-capturing
	// closures (escapedRefs == 0); their chunk-shape caches stay warm.
	vmPool sync.Pool
}

func (meta *chunkSharedMeta) prepare(chunk Chunk) {
	meta.once.Do(func() {
		cache := map[string]vmTypeSpec{}
		spec := func(typ string) vmTypeSpec {
			typ = strings.TrimSpace(typ)
			if s, ok := cache[typ]; ok {
				return s
			}
			s := parseVMTypeSpec(typ)
			cache[typ] = s
			return s
		}
		functions := append([]FunctionInfo(nil), chunk.Functions...)
		hasPoly := false
		for i := range functions {
			f := &functions[i]
			f.typeParamSet = functionTypeParameterSetOrNil(*f)
			f.requiresParamValidation = functionRequiresParamValidation(*f)
			if f.Async || f.IsGenerator || len(f.Decorators) > 0 {
				hasPoly = true
			}
			if len(f.ParamTypes) == 0 {
				continue
			}
			f.paramTypeSpecs = make([]vmTypeSpec, len(f.ParamTypes))
			for j, typ := range f.ParamTypes {
				if typ != "" {
					f.paramTypeSpecs[j] = spec(typ)
				}
			}
		}
		if !hasPoly {
			for _, classInfo := range chunk.Classes {
				if len(classInfo.Decorators) > 0 || len(classInfo.MethodDecorators) > 0 {
					hasPoly = true
					break
				}
			}
		}
		classIndex := make(map[string]int, len(chunk.Classes))
		for i, classInfo := range chunk.Classes {
			classIndex[strings.ToLower(classInfo.Name)] = i
		}
		var asserts map[int64]vmTypeSpec
		for _, instruction := range chunk.Instructions {
			if instruction.Op != OpTypeAssert || len(instruction.Operands) != 1 {
				continue
			}
			constIdx := instruction.Operands[0]
			if constIdx < 0 || int(constIdx) >= len(chunk.Constants) {
				continue
			}
			typeStr, ok := chunk.Constants[constIdx].(runtime.String)
			if !ok {
				continue
			}
			if asserts == nil {
				asserts = map[int64]vmTypeSpec{}
			}
			asserts[constIdx] = spec(typeStr.Value)
		}
		meta.preparedFunctions = functions
		meta.classIndex = classIndex
		meta.typeAssertSpecs = asserts
		meta.requiresCallSitePolymorphism = hasPoly
	})
}

// wrapperTemplate returns the shifted template for a wrapper of the
// given shape, building it on first use. The parent chunk must carry
// this meta (already prepared by NewVM).
func (meta *chunkSharedMeta) wrapperTemplate(parent Chunk, shift int, constShift int) *wrapperTemplate {
	key := wrapperKey{shift: shift, constShift: constShift}
	meta.mu.Lock()
	if tpl, ok := meta.wrappers[key]; ok {
		meta.mu.Unlock()
		return tpl
	}
	meta.mu.Unlock()

	built := buildWrapperTemplate(parent, meta, shift, constShift)

	meta.mu.Lock()
	defer meta.mu.Unlock()
	if tpl, ok := meta.wrappers[key]; ok {
		return tpl
	}
	if meta.wrappers == nil {
		meta.wrappers = map[wrapperKey]*wrapperTemplate{}
	}
	meta.wrappers[key] = built
	return built
}

func buildWrapperTemplate(parent Chunk, parentMeta *chunkSharedMeta, shift int, constShift int) *wrapperTemplate {
	functions := append([]FunctionInfo(nil), parentMeta.preparedFunctions...)
	for i := range functions {
		functions[i].Entry += int64(shift)
		functions[i].DefaultConstants = append([]int64(nil), functions[i].DefaultConstants...)
		for j, defaultIndex := range functions[i].DefaultConstants {
			if defaultIndex >= 0 {
				functions[i].DefaultConstants[j] = defaultIndex + int64(constShift)
			}
		}
	}
	classes := copyClasses(parent.Classes)
	for i := range classes {
		for j, defaultIndex := range classes[i].FieldDefaults {
			if defaultIndex >= 0 {
				classes[i].FieldDefaults[j] = defaultIndex + int64(constShift)
			}
		}
		for name, constantIndex := range classes[i].StaticValues {
			if constantIndex >= 0 {
				classes[i].StaticValues[name] = constantIndex + int64(constShift)
			}
		}
	}
	tail := make([]Instruction, 0, len(parent.Instructions))
	for _, instruction := range parent.Instructions {
		copied := instruction
		copied.Operands = append([]int64(nil), instruction.Operands...)
		shiftInstructionOperands(&copied, shift, constShift)
		tail = append(tail, copied)
	}
	derived := newChunkSharedMeta()
	derived.once.Do(func() {
		derived.preparedFunctions = functions
		derived.classIndex = parentMeta.classIndex
		derived.requiresCallSitePolymorphism = parentMeta.requiresCallSitePolymorphism
		if len(parentMeta.typeAssertSpecs) > 0 {
			shifted := make(map[int64]vmTypeSpec, len(parentMeta.typeAssertSpecs))
			for idx, s := range parentMeta.typeAssertSpecs {
				shifted[idx+int64(constShift)] = s
			}
			derived.typeAssertSpecs = shifted
		}
	})
	return &wrapperTemplate{functions: functions, classes: classes, tail: tail, meta: derived}
}

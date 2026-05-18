package runtime

import (
	"math/big"

	"geblang/internal/ast"
)

type cloneState struct {
	envs      map[*Environment]*Environment
	modules   map[*Module]*Module
	classes   map[*Class]*Class
	ifaces    map[*Interface]*Interface
	instances map[*Instance]*Instance
}

func CloneEnvironment(env *Environment) *Environment {
	state := newCloneState()
	return state.cloneEnvironment(env)
}

func CloneValue(value Value) Value {
	state := newCloneState()
	return state.cloneValue(value)
}

func CloneFunction(fn Function) Function {
	state := newCloneState()
	return state.cloneFunction(fn)
}

func newCloneState() *cloneState {
	return &cloneState{
		envs:      map[*Environment]*Environment{},
		modules:   map[*Module]*Module{},
		classes:   map[*Class]*Class{},
		ifaces:    map[*Interface]*Interface{},
		instances: map[*Instance]*Instance{},
	}
}

func (s *cloneState) cloneEnvironment(env *Environment) *Environment {
	if env == nil {
		return nil
	}
	if cloned, ok := s.envs[env]; ok {
		return cloned
	}
	cloned := &Environment{store: map[string]Binding{}}
	s.envs[env] = cloned
	cloned.outer = s.cloneEnvironment(env.outer)
	for name, binding := range env.store {
		cloned.store[name] = Binding{Value: s.cloneValue(binding.Value), Constant: binding.Constant}
	}
	return cloned
}

func (s *cloneState) cloneValue(value Value) Value {
	switch value := value.(type) {
	case nil:
		return nil
	case Null:
		return value
	case Bool:
		return value
	case Int:
		return Int{Value: new(big.Int).Set(value.Value)}
	case Decimal:
		return Decimal{Value: new(big.Rat).Set(value.Value)}
	case Float:
		return value
	case String:
		return value
	case Bytes:
		return Bytes{Value: append([]byte(nil), value.Value...)}
	case List:
		elements := make([]Value, len(value.Elements))
		for i, element := range value.Elements {
			elements[i] = s.cloneValue(element)
		}
		return List{Elements: elements}
	case Dict:
		entries := make(map[string]DictEntry, len(value.Entries))
		for key, entry := range value.Entries {
			entries[key] = DictEntry{Key: s.cloneValue(entry.Key), Value: s.cloneValue(entry.Value)}
		}
		return Dict{Entries: entries}
	case Range:
		return Range{
			Start:     new(big.Int).Set(value.Start),
			End:       new(big.Int).Set(value.End),
			Exclusive: value.Exclusive,
			Step:      new(big.Int).Set(value.Step),
		}
	case Function:
		return s.cloneFunction(value)
	case OverloadedFunction:
		overloads := make([]Function, len(value.Overloads))
		for i, overload := range value.Overloads {
			overloads[i] = s.cloneFunction(overload)
		}
		return OverloadedFunction{Name: value.Name, Overloads: overloads}
	case *Module:
		return s.cloneModule(value)
	case BytecodeFunction:
		return value
	case BytecodeClosure:
		upvalues := make([]Value, len(value.Upvalues))
		for i, upvalue := range value.Upvalues {
			upvalues[i] = s.cloneValue(upvalue)
		}
		value.Upvalues = upvalues
		return value
	case BytecodeClass:
		return value
	case *BytecodeCell:
		if value == nil {
			return value
		}
		return &BytecodeCell{Value: s.cloneValue(value.Value)}
	case NativeObject:
		return value
	case Error:
		return value
	case Type:
		return value
	case *Class:
		return s.cloneClass(value)
	case *Interface:
		return s.cloneInterface(value)
	case *Instance:
		return s.cloneInstance(value)
	default:
		return value
	}
}

func (s *cloneState) cloneFunction(fn Function) Function {
	fn.Env = s.cloneEnvironment(fn.Env)
	fn.TypeParameters = append([]string(nil), fn.TypeParameters...)
	fn.TypeParamConstraints = cloneTypeParamConstraints(fn.TypeParamConstraints)
	return fn
}

func (s *cloneState) cloneModule(module *Module) *Module {
	if module == nil {
		return nil
	}
	if cloned, ok := s.modules[module]; ok {
		return cloned
	}
	cloned := &Module{Name: module.Name, Exports: map[string]Value{}}
	s.modules[module] = cloned
	for name, value := range module.Exports {
		cloned.Exports[name] = s.cloneValue(value)
	}
	return cloned
}

func (s *cloneState) cloneClass(class *Class) *Class {
	if class == nil {
		return nil
	}
	if cloned, ok := s.classes[class]; ok {
		return cloned
	}
	cloned := &Class{
		Name:                 class.Name,
		Doc:                  class.Doc,
		Module:               class.Module,
		TypeParameters:       append([]string(nil), class.TypeParameters...),
		TypeParamConstraints: cloneTypeParamConstraints(class.TypeParamConstraints),
		Fields:               append([]Field(nil), class.Fields...),
		Methods:              map[string][]Function{},
		StaticMethods:        map[string][]Function{},
		MethodMetadata:       map[string][]FunctionMetadata{},
		StaticMetadata:       map[string][]FunctionMetadata{},
		StaticValues:         map[string]Value{},
		Constructors:         make([]Function, len(class.Constructors)),
	}
	s.classes[class] = cloned
	cloned.Parent = s.cloneClass(class.Parent)
	cloned.Implements = make([]*Interface, len(class.Implements))
	for i, iface := range class.Implements {
		cloned.Implements[i] = s.cloneInterface(iface)
	}
	for name, methods := range class.Methods {
		cloned.Methods[name] = cloneFunctions(s, methods)
	}
	for name, methods := range class.StaticMethods {
		cloned.StaticMethods[name] = cloneFunctions(s, methods)
	}
	for name, metadata := range class.MethodMetadata {
		cloned.MethodMetadata[name] = append([]FunctionMetadata(nil), metadata...)
	}
	for name, metadata := range class.StaticMetadata {
		cloned.StaticMetadata[name] = append([]FunctionMetadata(nil), metadata...)
	}
	for name, value := range class.StaticValues {
		cloned.StaticValues[name] = s.cloneValue(value)
	}
	for i, constructor := range class.Constructors {
		cloned.Constructors[i] = s.cloneFunction(constructor)
	}
	cloned.Env = s.cloneEnvironment(class.Env)
	return cloned
}

func (s *cloneState) cloneInterface(iface *Interface) *Interface {
	if iface == nil {
		return nil
	}
	if cloned, ok := s.ifaces[iface]; ok {
		return cloned
	}
	cloned := &Interface{Name: iface.Name, Doc: iface.Doc, TypeParameters: append([]string(nil), iface.TypeParameters...), Methods: append([]*ast.FunctionSignature(nil), iface.Methods...)}
	s.ifaces[iface] = cloned
	cloned.Parents = make([]*Interface, len(iface.Parents))
	for i, parent := range iface.Parents {
		cloned.Parents[i] = s.cloneInterface(parent)
	}
	return cloned
}

func cloneTypeParamConstraints(in map[string]*ast.TypeRef) map[string]*ast.TypeRef {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*ast.TypeRef, len(in))
	for name, ref := range in {
		out[name] = cloneTypeRef(ref)
	}
	return out
}

func cloneTypeRef(ref *ast.TypeRef) *ast.TypeRef {
	if ref == nil {
		return nil
	}
	out := *ref
	if len(ref.Arguments) > 0 {
		out.Arguments = make([]*ast.TypeRef, len(ref.Arguments))
		for i, arg := range ref.Arguments {
			out.Arguments[i] = cloneTypeRef(arg)
		}
	}
	out.Left = cloneTypeRef(ref.Left)
	out.Right = cloneTypeRef(ref.Right)
	return &out
}

func (s *cloneState) cloneInstance(instance *Instance) *Instance {
	if instance == nil {
		return nil
	}
	if cloned, ok := s.instances[instance]; ok {
		return cloned
	}
	cloned := &Instance{Class: s.cloneClass(instance.Class), Fields: map[string]Value{}}
	s.instances[instance] = cloned
	for name, value := range instance.Fields {
		cloned.Fields[name] = s.cloneValue(value)
	}
	return cloned
}

func cloneFunctions(s *cloneState, functions []Function) []Function {
	out := make([]Function, len(functions))
	for i, fn := range functions {
		out[i] = s.cloneFunction(fn)
	}
	return out
}

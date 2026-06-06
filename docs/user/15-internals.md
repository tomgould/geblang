# Geblang Internals

This chapter describes how Geblang is implemented, written for readers who want
a structural walkthrough of a scripting language built in Go. It covers the full
pipeline from source text to execution, and the design choices made at each
layer.

All source paths below are relative to the repository root.

## The Pipeline

A Geblang program goes through these stages before any code executes:

```
source text
    |
    v
Lexer (internal/lexer)         -- produces a stream of tokens
    |
    v
Parser (internal/parser)       -- produces an AST
    |
    v
Semantic Analyzer (internal/semantic)  -- validates declarations
    |
    v
  two possible execution paths:

  [Bytecode Compiler]           -- preferred path
  (internal/bytecode/compiler)
    |
    v
  [VM]
  (internal/bytecode/vm)

  [Evaluator]                   -- compatibility / fallback path
  (internal/evaluator)
```

The bytecode path is tried first. If compilation succeeds the VM runs the
resulting chunk. If compilation fails because of a genuine static error the run
aborts; if it fails only because the compiler does not yet support a construct
(and `--vm-strict` is not set), the evaluator runs the AST directly. `--disable-vm`
forces the evaluator; `--vm-strict` forbids the fallback. The `--trace-exec`
flag prints which engine ran a given script.

---

## Tokens: `internal/token`

`token.Token` is the atom the lexer produces and the parser consumes. Each token
has a `Type` (an integer constant), a `Literal` string, a `Raw` string, and
source `Line`/`Column` fields.

Token types are simple integer constants declared in `token/token.go`: `Ident`,
`Int`, `Decimal`, `Float`, `String`, `Assign`, `Plus`, `Eq`, `And`, `Or`,
`LParen`, `LBracket`, `LBrace`, `Dot`, `Arrow`, `NullCoalesce`, `Range`,
`EOF`, and so on. The full list is about 80 types covering every syntactic atom
the language uses.

---

## Lexer: `internal/lexer`

`lexer.Lexer` turns a source string into a sequence of tokens. Its state is
minimal:

```go
type Lexer struct {
    input        string
    position     int       // current byte offset
    readPosition int       // next byte offset
    ch           rune      // current character
    line         int
    column       int
    pendingDocs  []string  // accumulated doc comments
}
```

`NextToken()` is the only interface the parser calls. It dispatches on the
current character using a large switch, emitting one token per call. Multi-byte
sequences like `==`, `!=`, `<=`, `**=`, `..`, `..<`, `?.`, and `??` are
handled by peeking one character ahead.

Identifiers and keywords share the same token type initially; the lexer checks a
keyword table before returning the token, upgrading `Ident` to the keyword type
where appropriate. String literals handle escape sequences and Unicode in the
lexer. Numeric literals support decimal underscores (`1_000_000`), hex
(`0xFF`), octal (`0o77`), and binary (`0b1010`) prefixes.

Doc comments (`///` or `/** ... */`) are accumulated in `pendingDocs` and
attached to the next non-comment token so the parser can associate them with the
declaration they annotate.

---

## AST: `internal/ast`

The AST defines the tree of nodes the parser builds.

### Core interfaces

```go
type Node interface {
    TokenLiteral() string
    String() string
}

type Statement interface {
    Node
    statementNode()
}

type Expression interface {
    Node
    expressionNode()
}
```

`ast.Program` holds `[]Statement` and is the root returned by every parse call.

### Statements and expressions

There are around 40 statement types (`DeclarationStatement` for `let`,
`FunctionStatement`, `ClassStatement`, `ReturnStatement`, `ForStatement`,
`IfStatement`, `TryStatement`, `ImportStatement`, `InitStatement`,
`ModuleStatement`, etc.) and a similar number of expression types (`Identifier`,
`IntegerLiteral`, `StringLiteral`, `CallExpression`, `SelectorExpression`,
`IndexExpression`, `InfixExpression`, `PrefixExpression`, `FunctionLiteral`,
`ListLiteral`, `DictLiteral`, and more).

### TypeRef

`TypeRef` is a recursive type annotation node used everywhere a type appears:

```go
type TypeRef struct {
    Token     token.Token
    Name      string      // base name, e.g. "list", "string", "T"
    Nullable  bool        // ?string
    Arguments []*TypeRef  // generic arguments: list<string> -> Arguments[0].Name="string"
    ListAlias bool        // T[] shorthand -- Name holds the element type
    Left      *TypeRef    // left side of a union/intersection operator
    Operator  string      // "|" or "&"
    Right     *TypeRef    // right side
}
```

`list<int>` produces `{Name:"list", Arguments:[{Name:"int"}]}`. `int[]`
produces `{Name:"int", ListAlias:true}`. `string|int` produces
`{Operator:"|", Left:{Name:"string"}, Right:{Name:"int"}}`.

---

## Parser: `internal/parser`

The parser is a hand-written Pratt (top-down operator precedence) parser. Its
main entry point is `parser.New(lexer).ParseProgram()`, which returns
`*ast.Program`.

### Pratt parsing

Each token type optionally has two parse functions registered against it:

- a **prefix** function, called when the token appears at the start of an
  expression (literal values, prefix operators, `(grouped expr)`)
- an **infix** function, called when the token appears between two expressions
  (binary operators, `[index]`, `(call)`, `.member`)

`parseExpression(precedence)` calls the prefix function for the current token,
then repeatedly calls infix functions while the next token has higher precedence
than `precedence`. This naturally handles operator precedence and associativity
without a grammar table.

### Precedence levels

```
lowest
assign          =  +=  -=  ...
ternary         ?
nullCoalesce    ??
logicalOr       ||
logicalAnd      &&
bitOr           |
bitXor          ^
bitAnd          &
equality        ==  !=  is  instanceof
compare         <  <=  >  >=  ..  ..<  as
shift           <<  >>
sum             +  -
product         *  /  //  %
power           **
prefix          !  -  ~  ++  --
postfix         ++  --
call            .  ?.  (  [
```

### Type reference parsing

`parseTypeRefFromCurrent()` handles the full type syntax: nullable prefix `?`,
generic arguments `<T, U>`, union `|` and intersection `&` operators, and the
list shorthand `[]`. It converts `int[]` into a `TypeRef` with
`ListAlias=true` and `Name="int"`.

### Statement parsing

Top-level statements are parsed with `parseStatement()`. When a keyword is seen
(`let`, `func`, `class`, `if`, `for`, `return`, etc.) the corresponding
dedicated parse function is called. Otherwise the statement is treated as an
expression statement. The parser requires semicolons to terminate most
statements, following C-style syntax.

---

## Semantic Analyzer: `internal/semantic`

`semantic.Analyzer` performs a lightweight pre-execution pass over the AST.
It does not do full type inference; its job is to catch structural errors that
neither the parser nor runtime is well-placed to handle.

```go
type Analyzer struct {
    diagnostics []Diagnostic
    scopes      []map[string]typeInfo
    functions   map[string][]methodInfo
    classes     map[string]classInfo
    interfaces  map[string]interfaceInfo
    aliases     map[string]typeInfo
}
```

`Analyze(program)` runs a sequence of structural passes:

1. **`collectTypeDeclarations`**: walks all statements to register functions,
   classes and interfaces so that forward references work.
2. **`validateTopLevelOverloads`** / **`validateClassOverloads`** /
   **`validateInterfaceOverloads`**: check that overloaded functions, methods,
   constructors, and interface methods have distinct signatures.
3. **`validateInterfaceImplementations`**: verifies that classes marked
   `implements Foo` actually provide every method the interface declares.
4. **`validateCastDunderReturns`**: checks cast dunder methods return the type
   they cast to.
5. A per-statement walk then enforces module-file rules (a file opening with
   `module name;` allows only declarative statements plus a single `init {}`
   block at the top level) and flags identifiers that shadow reserved built-in
   module names.

Errors are collected as `[]Diagnostic` rather than halting immediately, so the
caller receives all problems at once. The analyzer is intentionally not a full
type checker; deeper type errors surface from the bytecode compiler, and
`geblang check` reconciles the analyzer and compiler diagnostics.

---

## Runtime Values: `internal/runtime`

Every value a Geblang program can hold implements the `Value` interface:

```go
type Value interface {
    TypeName() string
    Inspect() string
}
```

`TypeName()` returns the runtime type string (`"int"`, `"string"`, `"list"`,
`"func"`, etc.). `Inspect()` returns a human-readable representation.

### Primitive types

| Go type | Geblang type | Notes |
|---------|-------------|-------|
| `Null{}` | `null` | singleton |
| `Bool{Value bool}` | `bool` | |
| `SmallInt{Value int64}` | `int` | int64 fast path used by the VM |
| `Int{Value *big.Int}` | `int` | arbitrary precision; overflow / large literals |
| `Decimal{Value *big.Rat}` | `decimal` | exact rational arithmetic |
| `Float{Value float64}` | `float` | IEEE 754 double |
| `String{Value string}` | `string` | UTF-8 |
| `Bytes{Value []byte}` | `bytes` | raw byte slice |

`SmallInt` is the int64-backed representation used wherever the value
fits in a signed 64-bit register; the bytecode VM hot path operates on
`SmallInt` without ever touching `runtime.Value` (see `VMValue` below).
`Int` is the arbitrary-precision fallback for overflow and for literals
that do not fit in int64.

`Decimal` uses `math/big.Rat` so that `0.1 + 0.2` produces `0.3`
exactly. Literal `3.14` parses as `Decimal`, not `Float`; `3.14f` is a
`Float`.

### Collection types

| Go type | Geblang type | Notes |
|---------|-------------|-------|
| `List{Elements []Value}` | `list` | ordered, mutable |
| `Dict{Entries map[string]DictEntry}` | `dict` | string-keyed, mutable |
| `Set{Elements map[string]SetEntry}` | `set` | unordered, mutable |
| `Range{Start, End *big.Int, ...}` | `range` | lazy integer sequence |

Dict entries are keyed internally by the string form of the key value, but the
original key value is preserved alongside each entry so the runtime can return
the original type.

### Callable types

`Function` is the evaluator's callable value. It holds the AST parameter list,
body, captured environment, and optional `Native` hook for Go-implemented
functions. `OverloadedFunction` wraps a slice of `Function` values that share a
name; dispatch selects the overload whose parameter count and types match the
call arguments.

`BytecodeFunction` and `BytecodeClosure` are the VM equivalents. They store an
index into the chunk's `Functions` table and (for closures) a slice of captured
upvalues.

### Object types

`Instance` represents a class instance. It holds a pointer to its class
descriptor and a map of field names to values.

`Module` holds an exported name-to-value map and is what `import` statements
bind.

### Special runtime values

`Generator` wraps either a pre-computed value slice or a `next` callback
function, and provides `Next()/(value, bool, error)` iteration. Generator
coroutines in the VM communicate through a Go channel pair.

`DateTimeInstant`, `DateTimeDuration`, `DateTimeZone`, `URLValue`,
`HTTPHeaders`, `HTTPCookie`, `TemplateValue`, and `TemplateEngine` are typed
opaque wrappers for domain-specific native values. They carry type names like
`"datetime.Instant"` so the runtime can dispatch methods on them correctly.

---

## Bytecode Format: `internal/bytecode/bytecode.go`

### Chunk

A `Chunk` is the compiled form of one source file or module:

```go
type Chunk struct {
    SourceHash   [32]byte      // SHA-256 of the source bytes
    Compiler     string        // version string, for cache invalidation
    Constants    []Value       // literal pool
    Instructions []Instruction // flat instruction array
    Functions    []FunctionInfo
    Classes      []ClassInfo
    Interfaces   []InterfaceInfo
    Exports      []ExportInfo
}
```

All functions, including the top-level script, share the same flat
`Instructions` array. Each `FunctionInfo` holds an `Entry` offset into that
array, so function calls are just an IP jump.

### Instruction

```go
type Instruction struct {
    Op       Op       // opcode byte
    Operands []int64  // variable-length operand list
    Line     int
    Column   int
}
```

Operands are 64-bit signed integers. Most opcodes take zero or one operand
(a constant pool index, a local slot, a jump target). Multi-operand instructions
are rare.

### Opcodes

The `Op` type is a `byte`. There are around 150 opcodes (many are
performance-specialised variants of a more general opcode). Representative
examples:

| Opcode | Action |
|--------|--------|
| `OpConstant` | push `Constants[operand]` onto the stack |
| `OpAdd` / `OpSub` / `OpMul` / `OpDiv` | pop two values, push result |
| `OpDefineGlobal` / `OpGetGlobal` / `OpSetGlobal` | named global access |
| `OpDefineLocal` / `OpGetLocal` / `OpSetLocal` | slot-indexed local access |
| `OpCall` | call top-of-stack function with N args |
| `OpMethodCall` | call named method on receiver |
| `OpNativeCall` | call a registered native function |
| `OpBuildList` / `OpBuildDict` / `OpBuildSet` | pop N values, push collection |
| `OpJump` / `OpJumpIfFalse` | unconditional / conditional branch |
| `OpReturn` | pop return value, restore frame |
| `OpPushExceptionHandler` / `OpPopExceptionHandler` | try/catch frame management |
| `OpThrow` | raise an error value |
| `OpYield` | suspend a generator, send a value to the caller |
| `OpAwait` | suspend an async function until a Task resolves |
| `OpTypeAssert` | runtime check that the top-of-stack value matches a type string |
| `OpMakeClosure` | wrap a function with captured upvalues |
| `OpSetTypeBindings` | bind generic type parameters for the current call frame |
| `OpImportModule` | load and cache a module by name |
| `OpConstructClass` | allocate an instance and call its constructor |
| `OpDefineClass` | register a class descriptor in the global table |

### FunctionInfo

```go
type FunctionInfo struct {
    Name             string
    TypeParameters   []string   // generic parameter names
    Entry            int64      // offset into Instructions
    ParamNames       []string
    ParamSlots       []int64    // local slot index for each parameter
    ParamTypes       []string   // type strings for runtime checks
    ReturnType       string
    DefaultConstants []int64    // constant pool indices for default values
    UpvalueCount     int64
    LocalCount       int64      // slots to reserve for a call frame
    SharesParentFrame bool      // nested function reusing the parent's slots
    Variadic         bool
    Async            bool
    IsGenerator      bool
    Decorators       []DecoratorMetadata
}
```

### Serialization

`bytecode.Encode(chunk)` serializes a `Chunk` to bytes. The format starts with
the magic bytes `"GEBBC"`, a 2-byte version number, the SHA-256 source hash, and
then length-prefixed sections for constants, instructions, functions, classes,
interfaces, and exports. `bytecode.Decode(bytes)` is the inverse.

The source hash lets the runtime skip recompilation: if the cached `.gbc` file's
hash matches the source file, the cached chunk is loaded directly.

---

## Bytecode Compiler: `internal/bytecode/compiler.go`

`bytecode.Compile(program, source, version)` is the entry point. It creates a
`Compiler`, walks the AST, and returns a `Chunk`.

### Compiler state

```go
type Compiler struct {
    chunk         Chunk
    loops         []loopContext      // break/continue target stack
    globals       map[string]int64   // name to constant pool index
    globalTypes   map[string]string  // name to type string
    scopes        []map[string]binding // lexical scope stack
    locals        int64              // next local slot
    funcs         map[string][]int64 // name to overload function indices
    classes       map[string]int64   // name to class index
    interfaces    map[string]int64
    enums         map[string]int64
    typeAliases   map[string]*ast.TypeRef
    inFunc        int                // nesting depth for functions
    classStack    []int64            // for `this` inside methods
    finalizers    []finalizerContext // defer/finally cleanup stack
    expectedTypes []string           // declared types of let bindings in scope
    returnTypes   []string           // expected return types of enclosing functions
    reflectFuncs  map[string]DecoratorTarget // decorator metadata
    ...
}
```

### Compilation pass

The compiler does a single forward pass over `program.Statements`. At the end it
patches all forward-reference jump targets by back-filling jump instruction
operands.

For each statement kind the compiler emits a sequence of instructions. A
function declaration, for example:

1. Appends a new `FunctionInfo` entry to `chunk.Functions`.
2. Saves the current emit position and emits an `OpJump` placeholder to skip
   over the function body.
3. Records the function entry point, then compiles the body.
4. Emits `OpReturn` at the end.
5. Patches the `OpJump` operand to point past the function body.
6. Emits `OpDefineGlobal` (or `OpDefineLocal`) to bind the function value in the
   current scope.

Local variable scopes are managed with a stack of maps (`scopes`). Each `let`
binding allocates the next `locals` slot, recorded in the innermost scope map.
Reads and writes emit `OpGetLocal`/`OpSetLocal` with the slot number. At scope
exit the slot count is not reclaimed immediately (the VM allocates a fixed local
array per call frame).

Closures are compiled as functions with a non-zero `UpvalueCount`. The compiler
identifies captured variables and emits `OpMakeClosure` with a list of upvalue
indices.

Type strings (for `OpTypeAssert`, `ParamTypes`, `ReturnType`) are produced by
`bytecodeTypeNameForParam`, which flattens a `TypeRef` tree into a string like
`"list<int>"`, `"?string"`, or `"dict<string,list<float>>"`. Nested generic
types are serialized with brackets, and the VM parses them back with
bracket-depth-aware helpers.

---

## VM: `internal/bytecode/vm.go`

### VM state

```go
type VM struct {
    chunk             Chunk
    stdout            io.Writer
    stack             []runtime.VMValue // operand stack (tagged union)
    globals           []runtime.VMValue // indexed by global slot
    localsStack       []runtime.VMValue // all frames' locals in one slice
    currentFrameBP    int               // base pointer: this frame's slot 0
    frames            []callFrame       // call stack
    defers            [][]deferredAction
    exceptionHandlers []exceptionHandler
    pendingThrow      *runtime.Error
    moduleLoader      ModuleLoader
    statefulNative    StatefulNativeCaller
    natives           *native.Registry
    ...
}
```

Locals for every active frame live in a single `localsStack`; each frame's
slot `n` is `localsStack[currentFrameBP + n]`. A call advances the base
pointer rather than copying a per-frame slice (see Call frame, below).

The stack / locals / globals slices hold `runtime.VMValue`
(`internal/runtime/vmvalue.go`), a 32-byte tagged union with an inline
int64 payload and an interface-typed boxed fallback:

```go
type VMValue struct {
    Kind  VMKind        // 1-byte tag (Null, Bool, SmallInt, Float, Boxed, ...)
    I64   int64         // SmallInt payload / bool 0|1 / Float64bits
    Boxed runtime.Value // catch-all for List, Dict, Class, Instance, Int, Decimal, ...
}
```

This representation removes the two `runtime.SmallInt` heap
allocations per integer arithmetic step that a `runtime.Value`
interface previously imposed. The fast paths for `OpAddInt`,
`OpSubInt`, `OpLessInt`, the fused jump-compare opcodes, and the
load-op-store opcodes all operate on the `Kind == VMKindSmallInt`
case inline. Any non-primitive value takes the `VMKindBoxed`
fallback and routes through the same `runtime.Value` interface
used by the evaluator, preserving identity through `ToValue()` /
`VMValueFromValue()` at the boundary.

### Call frame

```go
type callFrame struct {
    returnIP     int
    basePointer  int                // this frame's slot 0 in localsStack
    localCount   int                // slots reserved for this frame
    typeBindings map[string]string  // generic type parameter bindings
    generator    chan vmGeneratorItem
    functionName string
    callLine     int
    ...
}
```

Each function call pushes a new `callFrame`. `returnIP` is the instruction index
to resume after the call returns. `basePointer` records where this frame's
locals begin in the shared `localsStack`, and `localCount` how many slots it
reserves; on return the VM restores the caller's base pointer and truncates the
locals stack. `typeBindings` holds generic type parameter names resolved to
concrete type strings for the duration of the call, populated by
`OpSetTypeBindings`.

### Run loop

`vm.Run()` is a simple fetch-decode-execute loop:

```go
ip := 0
for ip < len(vm.chunk.Instructions) {
    instr := vm.chunk.Instructions[ip]
    ip++
    switch instr.Op {
    case OpConstant:
        vm.pushVM(runtime.VMValueFromValue(vm.chunk.Constants[instr.Operands[0]]))
    case OpAddInt:
        // SmallInt fast path: operate on the inline I64 field, no boxing.
        ...
    // ... ~240 cases
    }
}
```

The stack grows upward; `pushVM` appends a `VMValue` to the slice and `popVM`
removes from the end. All arithmetic, comparisons, collections, control flow,
function calls, and error handling are implemented as opcode handlers in this
loop.

A handful of opcodes operate on `VMValue` directly without round-tripping
through `runtime.Value`:

- **Integer fast paths**: `OpAddInt`, `OpSubInt`, `OpMulInt`,
  `OpModInt`, `OpLessInt`, `OpGreaterInt`, `OpEqualInt`, `OpIncLocalInt`,
  `OpDecLocalInt`. Each checks `Kind == VMKindSmallInt` on its operands
  and performs the arithmetic on the inline `I64` field. Overflow falls
  back to `runtime.Int` (big.Int) promotion.
- **Fused compare-and-branch**: `OpJumpIfNotLessInt`,
  `OpJumpIfNotEqualInt`, etc. A single opcode pops two SmallInts and
  jumps when the underlying boolean condition is false, removing the
  intermediate Bool push/pop.
- **Fused load-op-store**: `OpAddLocalIntLocal`, `OpAddGlobalIntConst`,
  etc. Cover the `a = a + b` / `a = a + N` pattern with a single
  instruction.

The compiler emits these specialised opcodes whenever both operands
are statically typed as `int`; otherwise the generic dispatch path
runs and routes through `runtime.Value`.

### Function calls

`OpCall` pops N arguments and the callee value, then dispatches:

- If the callee is a `runtime.Function` (evaluator-style), it calls back into
  the evaluator via `StatefulNativeCaller`.
- If the callee is a `FunctionInfo` index, it pushes a new `callFrame` with the
  function's locals pre-allocated, sets `ip` to `Entry`, and continues the loop.
- For native functions, `startFunction` calls the registered Go function
  directly.
- For closures, captured upvalues are loaded into the new frame's leading
  slots before entering the body. Upvalues are shared `runtime.BytecodeCell`
  boxes, so an assignment to a captured variable is visible through every
  closure that captured it; a fresh `let` (including one re-run each loop
  iteration) binds a new value rather than writing through an existing box.

Type checking at call sites is done in `startFunction` using
`matchVMValueToTypeSpec`, which matches a value against a parsed type spec.
Generic type parameters in `typeBindings` are skipped during element-level
checks.

#### Frame-local storage

The VM keeps every active frame's locals in one shared `localsStack` slice and
gives each frame a `basePointer` into it, so entering a callee advances the base
pointer instead of copying a per-frame slice. A call reserves `localCount` slots
above the base pointer (growing the slice when needed) and a return truncates
back to the caller's region. This replaced an earlier per-call deep copy of the
locals slice that showed up as `runtime.duffcopy` in profiles of
recursion-heavy workloads.

### Generators

A generator function runs in a separate goroutine. When the VM compiles a
generator call it creates a Go channel pair. The generator goroutine runs the
function body, sending each yielded value on the channel. The outer VM receives
values through `OpIterNext`, which reads from the channel, and signals
completion through `OpIterClose`.

### Exception handling

`OpPushExceptionHandler` pushes an `exceptionHandler` onto a stack, recording
the instruction offset of the handler code. When an error is thrown (either by
`OpThrow` or a runtime operation), the VM unwinds to the nearest handler and
jumps to its offset. `OpPopExceptionHandler` removes the handler when the
protected block exits normally. Uncaught throws propagate as a Go error from
`Run()`.

### Deferred calls

`defer` statements push `deferredAction` values onto a per-scope slice. When a
function returns or a try block exits, the VM pops and executes all deferred
actions in reverse order.

### Module loading

`OpImportModule` calls `ModuleLoader.LoadModule(canonical, alias)`. The loader
compiles (or loads from cache) the target module's chunk, runs it in a fresh VM,
and returns the exported `runtime.Module`. Modules are cached after first load.

---

## Evaluator: `internal/evaluator`

The evaluator is a tree-walking interpreter that executes the AST directly
without compilation. It is the runtime for `geblang test`, the `--disable-vm`
mode, and the fallback when the bytecode compiler does not yet support a
construct. The evaluator and VM are held to strict output parity (enforced by
the parity and fuzz suites in `internal/bytecode`); a language feature is not
considered done until both backends implement it and parity tests pass.

The main types are `Evaluator` (holds stdlib registrations, import caches, and
the evaluator configuration) and `Session` (wraps an `Evaluator` and an
`Environment` for one REPL session or script run).

`evalExpression(expr, env)` and `evalStatements(stmts, env)` are the core
recursive functions. They return a `runtime.Value` and an `error`.

Control-flow signals (return, break, continue, throw, exit) are communicated
as a `signal` struct rather than a Go error, avoiding the need to unwrap
errors at each level:

```go
type signal struct {
    kind     string        // "return", "break", "continue", "throw"
    value    runtime.Value // return or thrown value
    thrown   runtime.Value
    exited   bool
    exitCode int
}
```

The evaluator handles all the same language features as the VM but through direct
Go function calls rather than opcode dispatch.

### Builtin modules

The evaluator implements stateful standard library modules (HTTP client, database,
filesystem, Redis, templates, etc.) as Go structs with method maps. Each module
is registered in `e.builtins` under its canonical name and provides a
`map[string]builtinFunction` of callable entries. Module functions receive
`[]runtime.Value` arguments and return `(runtime.Value, error)`.

---

## Native Registry: `internal/native`

`native.Registry` holds pure, stateless functions shared between the evaluator
and the VM. These are functions whose behavior depends only on their arguments
and produces no side effects beyond their return value: math, string
manipulation, encoding, parsing, cryptography, and similar utilities.

Registering a native function associates a module name, a function name, and a
Go `func([]runtime.Value) (runtime.Value, error)` implementation. The registry
is initialized once and passed to both the evaluator and the VM.

The evaluator calls `registry.Call(module, name, args)` directly. The VM emits
`OpNativeCall` with the module and name embedded as constants, and resolves the
call through the same registry at runtime.

---

## Module System: `internal/modules`

`modules.Resolver` locates `.gb` source files for import statements.

```go
type Resolver struct {
    ModulePaths   []string  // user-specified search paths
    StdlibPaths   []string  // stdlib installation paths
    DisableStdlib bool
    Manifests     map[string]*Manifest
}
```

Resolution order (`Resolver.Resolve`):

1. A `geblang.` prefix (`import geblang.io`) resolves strictly against the
   stdlib, never user or package files. Declaring a module named `geblang` (or
   `geblang.*`) is rejected.
2. A reserved built-in name (a native module or a stdlib module shipped with the
   toolchain) resolves only to the built-in; user or package files may not
   shadow it. This keeps resolution identical on both backends (the VM always
   treats these names natively).
3. Otherwise, search the configured search paths in order -- `ModulePaths`, then
   `StdlibPaths` (unless stdlib is disabled), then any `GEBLANG_PATH` entries --
   for `<name>.gb` or `<name>/init.gb` (dotted names map to nested directories).
4. Search the module roots of any package dependencies declared in manifests.
5. Fall back to a path relative to the current working directory.

A `Manifest` (`geblang.yaml`) can declare a module name, version, source
entrypoint, resource globs, additional module paths, and dependencies. The
resolver loads manifests to support multi-file packages.

---

## Source Stdlib: `stdlib/`

Not all standard library code is written in Go. Several modules are written in
Geblang itself and distributed as source files in the `stdlib/` directory:

| File / directory | Module |
|-----------------|--------|
| `stdlib/option.gb` | `option` (`Option<T>` type) |
| `stdlib/result.gb` | `result` (`Result<T, E>` type) |
| `stdlib/pathlib.gb` | `pathlib` (path manipulation) |
| `stdlib/mailer.gb` | `mailer` (high-level mailer) |
| `stdlib/config.gb` | `config` (typed configuration loading) |
| `stdlib/redis.gb` | `redis` (Redis client wrapper) |
| `stdlib/functools.gb` | `functools` (pipe / compose / partial / memoize) |
| `stdlib/http/` | HTTP server utilities |
| `stdlib/web/` | Web framework helpers |
| `stdlib/cli/` | CLI argument parsing, `cli.color` ANSI styling |
| `stdlib/async/` | Async utilities, `async.rate` throttle/debounce |
| `stdlib/testing/` | Test runner and assertions |
| `stdlib/schema/` | Schema validation |

These modules are installed alongside the `geblang` binary and are found via
`StdlibPaths`. They are compiled and cached like any other user module.

---

## CLI: `cmd/geblang`

The `geblang` binary entry point is `cmd/geblang/main.go`. It parses command-
line arguments and delegates to one of several execution modes:

- **Script mode**: `geblang script.gb` reads and parses the file, runs the
  semantic analyzer, then tries the bytecode path and falls back to the
  evaluator. If the file declares an exported top-level `main`, it is
  auto-invoked after analysis (arguments forwarded, an `int` return becomes the
  exit code); a file without an exported `main` runs its top-level statements.
- **Module mode**: `geblang -m moduleName` generates a thin wrapper script
  that imports the named module and calls its `main()` function. Use this to run
  a module by canonical name; running the file directly auto-invokes `main` too.
- **REPL mode**: `geblang` with no arguments starts an interactive session.
  The REPL (`repl.go`) maintains a persistent `evaluator.Session`. Each input
  line is parsed and evaluated; expression results are printed if non-void.
- **Check mode**: `geblang check script.gb` runs parse and semantic analysis
  only, reporting errors without executing.
- **Format mode**: `geblang fmt script.gb` runs the formatter
  (`internal/formatter`).
- **Build mode**: `geblang build` compiles the application and its
  dependencies into a self-contained binary (`build.go`, `internal/bundle`).
- **LSP mode**: `geblang lsp` starts the Language Server Protocol server
  (`lsp.go`, `internal/lsp`).
- **DAP mode**: `geblang dap` starts the Debug Adapter Protocol server
  (`dap.go`, `internal/dap`).

### Execution mode selection

`runScript` tries the bytecode path by calling `loadOrCompileBytecode`, which
either decodes a cached `.gbc` file (if the source hash matches) or calls
`bytecode.Compile`. If compilation succeeds, a `VM` is constructed and `Run()`
is called. If compilation fails only because a construct is not yet implemented
in the compiler, the function falls back to `runEvaluator` (unless `--vm-strict`
was passed); a genuine static error aborts instead of falling back. `--disable-vm`
skips the bytecode path entirely.

The `--trace-exec` flag writes a one-line note to stderr indicating which path
(`vm` or `evaluator`) handled the script and, on the evaluator path, the
compilation error that triggered the fallback.

### Bytecode module loader

The VM calls into a `ModuleLoader` implementation (`bytecodeModuleLoader` in
`main.go`) for each `OpImportModule` instruction. The loader:

1. Resolves the module name to a file path via `modules.Resolver`.
2. Reads and parses the source file.
3. Compiles it to a `Chunk` (or loads from the `.gbc` cache).
4. Creates a fresh `VM`, runs the chunk, and collects the exported values.
5. Caches the resulting `runtime.Module` so subsequent imports of the same
   module in the same process return the cached value.

For modules that use stateful native functions (HTTP, database, etc.) the loader
holds a shared `evaluator.Evaluator` instance and routes `StatefulNativeCaller`
calls through it.

---

## Adding a New Feature

The typical path for adding a language feature is:

1. Add any new token types to `internal/token/token.go`.
2. Update `internal/lexer/lexer.go` to emit the new tokens.
3. Add AST node types to `internal/ast/ast.go`.
4. Update `internal/parser/parser.go` to parse the new syntax into AST nodes.
5. Update `internal/semantic/analyzer.go` if the feature involves declarations
   that need structural validation.
6. Implement the feature in `internal/evaluator/evaluator.go`.
7. Add opcode(s) to `internal/bytecode/bytecode.go`.
8. Emit the opcodes in `internal/bytecode/compiler.go`.
9. Handle the opcodes in `internal/bytecode/vm.go`.
10. Add parity tests in `internal/bytecode/parity_test.go` to verify that the
    evaluator and VM produce the same result, plus a Geblang-level test under
    `tests/`.
11. Update documentation in `docs/user/` and add examples in `examples/`.

A feature may be prototyped in the evaluator first, but it is not finished until
both backends implement it and the parity (and fuzz) tests agree. Backend
divergence is treated as a bug: the evaluator's fallback exists for constructs
the compiler does not yet handle, not as a place for permanent behaviour
differences.

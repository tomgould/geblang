package evaluator

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/big"
	mrand "math/rand"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"geblang/internal/ast"
	"geblang/internal/concurrent"
	"geblang/internal/ffi"
	"geblang/internal/lexer"
	"geblang/internal/modules"
	"geblang/internal/native"
	"geblang/internal/parser"
	"geblang/internal/runtime"
	"geblang/internal/semantic"
	"geblang/internal/version"

	tomllib "github.com/BurntSushi/toml"
	"github.com/creack/pty"
	"github.com/fsnotify/fsnotify"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pkg/sftp"
	amqp091 "github.com/rabbitmq/amqp091-go"
	kafkago "github.com/segmentio/kafka-go"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/sys/unix"
	yamllib "gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

type Result struct {
	ExitCode int
	Exited   bool
}

const DefaultMaxCallDepth = 10000

type Evaluator struct {
	stdout               io.Writer
	stderr               io.Writer
	stdin                io.Reader
	stdinReader          *bufio.Reader
	imports              map[string]bool
	importNames          map[string]string
	modulePaths          []string
	modules              map[string]*runtime.Module
	loading              map[string]bool
	modulePrograms       map[string]*ast.Program
	manifests            map[string]*packageManifest
	typeAliases          map[string]*ast.TypeRef
	natives              *native.Registry
	builtins             map[string]map[string]builtinFunc
	testClass            *runtime.Class
	httpRequestClass     *runtime.Class
	httpResponseClass    *runtime.Class
	httpClientClass      *runtime.Class
	httpBuilderClass     *runtime.Class
	httpCookieJarClass   *runtime.Class
	httpFetchStreamClass *runtime.Class
	processClass         *runtime.Class
	processResultClass   *runtime.Class
	dbConnectionClass    *runtime.Class
	dbTransactionClass   *runtime.Class
	dbStatementClass     *runtime.Class
	dbRowsClass          *runtime.Class
	streamIfaces         map[string]*runtime.Interface
	errorClassParents    map[string]string
	// globalClasses is the cross-module class registry. Every user
	// class registers here at definition time so reflect.class(name)
	// from any module can find it.
	globalClasses map[string]*runtime.Class
	// Identifier name -> original class name for class identifiers
	// that a class decorator rebound to a callable. Used by
	// applyCallableValue to stamp the returned instance.
	decoratedClassIdents map[string]string
	errorSentinels       map[string]*runtime.Class
	deferFrames          []*deferFrame
	yieldFrames          []*yieldFrame
	callStack            []evalFrame
	callDepth            int
	maxCallDepth         int
	// classStack tracks the lexical class of the currently executing
	// method/constructor. `parent(...)` resolves to the parent of the top of
	// this stack rather than to `this.Class.Parent`, so that calling
	// `parent(msg)` inside e.g. AppError's body always dispatches to Error,
	// not back to AppError, regardless of the runtime class of `this`.
	classStack []*runtime.Class
	// destructibleInstances tracks instances of classes that
	// declare a `func ~ClassName()` destructor. The sweep at
	// Cleanup() time invokes their destructors in reverse-creation
	// order (LIFO). `del x` removes the corresponding entry and
	// fires the destructor immediately.
	destructibleInstances []*runtime.Instance
	args                  []string
	debugHook             DebugHookFunc
	debugSourcePath       string
	parent                *Evaluator
	AssertionsDisabled    bool
	vmDispatcher          MethodDispatcher
	dbMu                  sync.Mutex
	nextDBID              int64
	dbs                   map[int64]*sql.DB
	dbDrivers             map[int64]string
	nextTxID              int64
	txs                   map[int64]*dbTxHandle
	nextStmtID            int64
	stmts                 map[int64]*dbStmtHandle
	nextDBRowsID          int64
	dbRows                map[int64]*dbRowsHandle
	fileMu                sync.Mutex
	nextFileID            int64
	files                 map[int64]*os.File
	bufReaders            map[int64]*bufio.Reader
	bufferMu              sync.Mutex
	nextBufferID          int64
	buffers               map[int64]*bytes.Buffer
	streamMu              sync.Mutex
	nextStreamID          int64
	streams               map[int64]*ioStreamHandle
	processMu             sync.Mutex
	nextProcID            int64
	processes             map[int64]*processHandle
	logMu                 sync.Mutex
	nextLogID             int64
	loggers               map[int64]*loggerHandle
	metricsMu             sync.Mutex
	metrics               map[string]float64
	metricRegistry        map[string]*metricsEntry
	traceMu               sync.Mutex
	nextTraceID           int64
	traces                map[int64]*traceSpan
	watchMu               sync.Mutex
	nextWatchID           int64
	watches               map[int64]*watchHandle
	webMu                 sync.Mutex
	nextWebID             int64
	webApps               map[int64]*webApp
	wsMu                  sync.Mutex
	nextWSID              int64
	websockets            map[int64]*wsHandle
	amqpMu                sync.Mutex
	nextAmqpConnID        int64
	amqpConns             map[int64]*amqp091.Connection
	nextAmqpChanID        int64
	amqpChans             map[int64]*amqp091.Channel
	kafkaMu               sync.Mutex
	nextKafkaWriterID     int64
	kafkaWriters          map[int64]*kafkago.Writer
	nextKafkaReaderID     int64
	kafkaReaders          map[int64]*kafkaReaderHandle
	netMu                 sync.Mutex
	nextNetID             int64
	netHandles            map[int64]*netHandle
	netServerMu           sync.Mutex
	nextNetServerID       int64
	netServers            map[int64]*netServerHandle
	sshMu                 sync.Mutex
	nextSSHID             int64
	sshClients            map[int64]*sshClientHandle
	sshSessions           map[int64]*sshSessionHandle
	sshTunnels            map[int64]*sshTunnelHandle
	httpServerMu          sync.Mutex
	nextHTTPServerID      int64
	httpServers           map[int64]*httpServerHandle
	httpStreamMu          sync.Mutex
	nextHTTPStreamID      int64
	httpStreams           map[int64]*httpStreamHandle
	httpClientMu          sync.Mutex
	nextHTTPClientID      int64
	httpClientHandles     map[int64]*httpClientHandle
	httpCookieJarMu       sync.Mutex
	nextCookieJarID       int64
	httpCookieJars        map[int64]http.CookieJar
	httpFetchStreamMu     sync.Mutex
	nextFetchStreamID     int64
	httpFetchStreams      map[int64]*httpFetchStreamHandle
	jsonMu                sync.Mutex
	nextJSONID            int64
	jsonReaders           map[int64]*jsonStreamReader
	xmlMu                 sync.Mutex
	nextXMLID             int64
	xmlReaders            map[int64]*xmlStreamReader
	csvMu                 sync.Mutex
	nextCSVID             int64
	csvReaders            map[int64]*csvStreamReader
	yamlMu                sync.Mutex
	nextYAMLID            int64
	yamlReaders           map[int64]*yamlStreamReader
	extMu                 sync.Mutex
	nextExtID             int64
	extConns              map[int64]*extHandle
	ffi                   *ffiState
}

type builtinFunc func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error)

// MethodDispatcher is implemented by the VM so the evaluator can call methods
// on VM-owned instances and run test classes compiled to bytecode.
type MethodDispatcher interface {
	HasInstanceMethod(instance *runtime.Instance, name string) bool
	CallInstanceMethod(instance *runtime.Instance, name string, args []runtime.Value) (runtime.Value, error)
	RunTestClass(classIndex int64, tagFilter []string) (runtime.Value, error)
	PatchNative(module, name string, fn native.Function)
	UnpatchNative(module, name string)
	NativeSnapshot() map[string]native.Function
	RestoreNatives(snapshot map[string]native.Function)
}

type httpClientHandle struct {
	client  *http.Client
	baseURL string
	headers http.Header
}

type httpFetchStreamHandle struct {
	ch    chan runtime.Value
	total int
	mu    sync.Mutex
	read  int
}

type dbRowsHandle struct {
	rows      *sql.Rows
	columns   []string
	current   runtime.Value
	cache     []runtime.Value
	closed    bool
	exhausted bool
}

type dbStmtHandle struct {
	stmt       *sql.Stmt
	driver     string
	paramNames []string
}

type dbTxHandle struct {
	tx     *sql.Tx
	driver string
}

type processHandle struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	cancel context.CancelFunc
}

type ioStreamHandle struct {
	name      string
	reader    io.Reader
	writer    io.Writer
	closer    io.Closer
	memory    *memoryStream
	restore   func()
	closed    bool
	bufReader *bufio.Reader
}

type memoryStream struct {
	mu   sync.Mutex
	data []byte
	pos  int
}

func (m *memoryStream) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pos >= len(m.data) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *memoryStream) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append(m.data, p...)
	return len(p), nil
}

func (m *memoryStream) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.data)
}

func (m *memoryStream) Bytes() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.data...)
}

func (m *memoryStream) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = nil
	m.pos = 0
}

type netHandle struct {
	listener net.Listener
	conn     net.Conn
	packet   net.PacketConn
}

// netServerHandle owns an accept-loop goroutine for net.serve. The
// goroutine exits when the listener is closed; stopServerHandle
// closes the listener and waits for the goroutine to drain.
type netServerHandle struct {
	listener net.Listener
	wg       sync.WaitGroup
	stopped  bool
	pool     *concurrent.Pool
}

// sshClientHandle wraps an established ssh.Client plus a lazily
// created sftp.Client (shared across SFTP calls on the same
// connection).
type sshClientHandle struct {
	client  *ssh.Client
	sftpMu  sync.Mutex
	sftpCli *sftp.Client
	closed  bool
}

// sshSessionHandle wraps a long-running ssh.Session whose stdin /
// stdout / stderr have been wired to streamable pipes. Mirrors
// processHandle for proc.spawn.
type sshSessionHandle struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	stderr  io.Reader
}

// sshTunnelHandle owns the accept-loop goroutine for a local or
// remote port forward.
type sshTunnelHandle struct {
	listener net.Listener
	wg       sync.WaitGroup
	stopped  bool
}

type httpServerHandle struct {
	server   *http.Server
	listener net.Listener
	done     chan error
	pool     *concurrent.Pool
	certPEM  []byte // served TLS certificate (PEM); nil for plain HTTP
}

type httpStreamHandle struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	closed  bool
}

type jsonStreamReader struct {
	decoder *json.Decoder
	stack   []jsonContext
	pending runtime.Value
	done    bool
}

type jsonContext struct {
	kind      byte
	expectKey bool
}

type xmlStreamReader struct {
	decoder *xml.Decoder
	pending runtime.Value
	done    bool
	source  string
	depth   int
	roots   int
}

type csvStreamReader struct {
	reader  *csv.Reader
	pending runtime.Value
	done    bool
	row     int64
}

type yamlStreamReader struct {
	decoder *yamllib.Decoder
	pending runtime.Value
	queue   []runtime.Value
	done    bool
}

type packageManifest struct {
	Path         string
	Root         string
	Name         string
	Version      string
	Source       string
	Paths        []string
	Dependencies map[string]packageDependency
	Extensions   map[string]*extConfig
	Permissions  permissionsBlock
}

type packageDependency struct {
	Path string `yaml:"path"`
}

func (d *packageDependency) UnmarshalYAML(value *yamllib.Node) error {
	switch value.Kind {
	case yamllib.ScalarNode:
		d.Path = value.Value
		return nil
	case yamllib.MappingNode:
		type dependency packageDependency
		var parsed dependency
		if err := value.Decode(&parsed); err != nil {
			return err
		}
		*d = packageDependency(parsed)
		return nil
	default:
		return fmt.Errorf("dependency must be a path string or mapping")
	}
}

type packageManifestFile struct {
	Name         string                       `yaml:"name"`
	Version      string                       `yaml:"version"`
	Source       string                       `yaml:"source"`
	Paths        []string                     `yaml:"paths"`
	ModulePaths  []string                     `yaml:"modulePaths"`
	Dependencies map[string]packageDependency `yaml:"dependencies"`
	Extensions   map[string]*extConfig        `yaml:"extensions"`
	Permissions  permissionsBlock             `yaml:"permissions"`
	Package      struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"package"`
}

type permissionsBlock struct {
	FFI *ffi.PolicyConfig `yaml:"ffi" json:"ffi"`
}

type processSpec struct {
	command string
	args    []string
	cwd     string
	env     map[string]string
	timeout time.Duration
}

type traceSpan struct {
	name         string
	start        time.Time
	end          time.Time
	ended        bool
	attrs        map[string]runtime.Value
	events       []traceEvent
	traceID      []byte // 16 bytes; empty for legacy spans created before OTLP support
	spanID       []byte // 8 bytes
	parentSpanID []byte // 8 bytes; empty for root spans
}

type traceEvent struct {
	name  string
	at    time.Time
	attrs map[string]runtime.Value
}

type webApp struct {
	routes            []webRoute
	beforeMiddlewares []runtime.Value
	middlewares       []runtime.Value
}

type webRoute struct {
	method  string
	path    string
	handler runtime.Value
}

type loggerHandle struct {
	target  string
	writer  io.Writer
	closer  io.Closer
	handler *runtime.Instance
}

type deferredCall struct {
	expr ast.Expression
	env  *runtime.Environment
}

type deferFrame struct {
	calls []deferredCall
}

type evalFrame struct {
	name string
	line int
	fn   *runtime.Function
	env  *runtime.Environment // environment at function entry (for per-frame variable inspection)
}

type generatorItem struct {
	value runtime.Value
	err   error
}

type yieldFrame struct {
	values []runtime.Value
	ch     chan generatorItem
	done   <-chan struct{}
}

type signal struct {
	value    runtime.Value
	thrown   *runtime.Error
	exited   bool
	exitCode int
	kind     string
}

type thrownError struct {
	value runtime.Error
}

func (e thrownError) Error() string {
	return e.value.Inspect()
}

// ErrorClass exposes the carried Geblang error class so the VM /
// other native call sites can preserve the typed throw across the
// native-to-script boundary via runtime.TypedError.
func (e thrownError) ErrorClass() string {
	return e.value.Class
}

func New(stdout io.Writer) *Evaluator {
	return NewWithArgs(stdout, nil)
}

func NewWithArgs(stdout io.Writer, args []string) *Evaluator {
	return NewWithArgsAndModulePaths(stdout, args, nil)
}

func NewWithArgsAndModulePaths(stdout io.Writer, args []string, modulePaths []string) *Evaluator {
	if stdout == nil {
		stdout = io.Discard
	}
	e := &Evaluator{stdout: stdout, stderr: os.Stderr, stdin: os.Stdin, imports: map[string]bool{}, importNames: map[string]string{}, modulePaths: append([]string(nil), modulePaths...), modules: map[string]*runtime.Module{}, loading: map[string]bool{}, modulePrograms: map[string]*ast.Program{}, manifests: map[string]*packageManifest{}, typeAliases: map[string]*ast.TypeRef{}, maxCallDepth: DefaultMaxCallDepth, args: append([]string(nil), args...), dbs: map[int64]*sql.DB{}, dbDrivers: map[int64]string{}, txs: map[int64]*dbTxHandle{}, stmts: map[int64]*dbStmtHandle{}, dbRows: map[int64]*dbRowsHandle{}, files: map[int64]*os.File{}, bufReaders: map[int64]*bufio.Reader{}, buffers: map[int64]*bytes.Buffer{}, streams: map[int64]*ioStreamHandle{}, processes: map[int64]*processHandle{}, loggers: map[int64]*loggerHandle{}, metrics: map[string]float64{}, metricRegistry: map[string]*metricsEntry{}, traces: map[int64]*traceSpan{}, watches: map[int64]*watchHandle{}, webApps: map[int64]*webApp{}, websockets: map[int64]*wsHandle{}, amqpConns: map[int64]*amqp091.Connection{}, amqpChans: map[int64]*amqp091.Channel{}, kafkaWriters: map[int64]*kafkago.Writer{}, kafkaReaders: map[int64]*kafkaReaderHandle{}, netHandles: map[int64]*netHandle{}, netServers: map[int64]*netServerHandle{}, sshClients: map[int64]*sshClientHandle{}, sshSessions: map[int64]*sshSessionHandle{}, sshTunnels: map[int64]*sshTunnelHandle{}, httpServers: map[int64]*httpServerHandle{}, httpStreams: map[int64]*httpStreamHandle{}, httpClientHandles: map[int64]*httpClientHandle{}, httpCookieJars: map[int64]http.CookieJar{}, httpFetchStreams: map[int64]*httpFetchStreamHandle{}, jsonReaders: map[int64]*jsonStreamReader{}, xmlReaders: map[int64]*xmlStreamReader{}, csvReaders: map[int64]*csvStreamReader{}, yamlReaders: map[int64]*yamlStreamReader{}, extConns: map[int64]*extHandle{}, ffi: newFFIState(), natives: native.NewBuiltinRegistry(), errorClassParents: map[string]string{}, errorSentinels: map[string]*runtime.Class{}, globalClasses: map[string]*runtime.Class{}, decoratedClassIdents: map[string]string{}}
	e.builtins = e.builtinModules()
	// Register an InstanceInvoker so native code (e.g.
	// convert.go's __serialize__ dispatch) can call class
	// methods. Latest-writer-wins; both backends populate this
	// at startup. See bytecode.NewVM for the VM counterpart.
	native.SetInstanceInvoker(e.invokeInstanceMethod)
	native.SetClassDeserializer(e.deserializeIntoClass)
	return e
}

// deserializeIntoClass implements native.ClassDeserializer for
// the tree-walking evaluator. Tries static __deserialize__ first;
// falls back to positional constructor calls with dict keys
// matched against constructor parameter names.
func (e *Evaluator) deserializeIntoClass(classValue runtime.Value, value runtime.Value) (runtime.Value, error) {
	class, ok := classValue.(*runtime.Class)
	if !ok {
		return nil, fmt.Errorf("deserialize: expected class, got %s", classValue.TypeName())
	}
	if method, ok := lookupStaticDunderEval(class, "__deserialize", "__deserialize__"); ok {
		return e.applyFunction(method, []runtime.Value{value})
	}
	dict, ok := value.(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("deserialize %s: expected dict, got %s", class.Name, value.TypeName())
	}
	if len(class.Constructors) == 0 {
		// Classes without an explicit constructor get their fields
		// populated directly from the dict, matching the implicit
		// "data class" shape. Missing keys leave the default; extra
		// keys are ignored.
		v, err := e.instantiateClass(class, nil)
		if err != nil {
			return nil, err
		}
		instance, ok := v.(*runtime.Instance)
		if !ok {
			return v, nil
		}
		for _, field := range class.Fields {
			key := runtime.String{Value: field.Name}
			entry, hit := dict.Entries[native.DictKey(key)]
			if hit {
				instance.Fields[field.Name] = entry.Value
			}
		}
		return instance, nil
	}
	ctor := class.Constructors[0]
	args := make([]runtime.Value, 0, len(ctor.Parameters))
	for _, param := range ctor.Parameters {
		paramName := ""
		if param.Name != nil {
			paramName = param.Name.Value
		}
		key := runtime.String{Value: paramName}
		entry, ok := dict.Entries[native.DictKey(key)]
		if ok {
			args = append(args, entry.Value)
			continue
		}
		if param.Default != nil {
			defaultVal, err := e.evalExpression(param.Default, runtime.NewEnclosedEnvironment(class.Env))
			if err != nil {
				return nil, fmt.Errorf("deserialize %s: default for %s: %w", class.Name, paramName, err)
			}
			args = append(args, defaultVal)
			continue
		}
		return nil, fmt.Errorf("deserialize %s: missing field %q", class.Name, paramName)
	}
	return e.instantiateClass(class, args)
}

// invokeInstanceMethod implements native.InstanceInvoker for the
// tree-walking evaluator. Returns (result, true, nil) when the
// method exists and was called, (nil, false, nil) when the class
// has no such method, (nil, false, err) on call error.
func (e *Evaluator) invokeInstanceMethod(instance *runtime.Instance, method string, args []runtime.Value) (runtime.Value, bool, error) {
	if instance == nil || instance.Class == nil {
		return nil, false, nil
	}
	fn, ok := lookupMethod(instance.Class, method)
	if !ok {
		return nil, false, nil
	}
	result, err := e.applyFunctionWithThis(fn, args, instance)
	if err != nil {
		return nil, false, err
	}
	return result, true, nil
}

func (e *Evaluator) SetMaxCallDepth(limit int) {
	e.maxCallDepth = limit
}

func (e *Evaluator) SetMethodDispatcher(d MethodDispatcher) {
	e.vmDispatcher = d
}

func (e *Evaluator) HandleDirectPrint() bool {
	return true
}

// nativeBuiltinValue wraps a pure native builtin (canonical.name) as a
// first-class callable value, or returns false if no such native exists.
// Gated on the pure-native registry so the VM resolves the identical set.
func (e *Evaluator) nativeBuiltinValue(canonical, name string) (runtime.Value, bool) {
	if e.natives.LookupKey(native.Key(canonical, name)) == nil {
		return nil, false
	}
	return e.wrapBuiltinAsFunction(canonical, name, e.registryBuiltin(canonical, name)), true
}

func (e *Evaluator) registryBuiltin(module, name string) builtinFunc {
	key := native.Key(module, name)
	return func(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
		return e.natives.CallKey(key, args)
	}
}

func (e *Evaluator) CallBuiltin(module, name string, args []runtime.Value, argNames []string) (runtime.Value, error) {
	if e.httpRequestClass == nil || e.httpResponseClass == nil {
		if err := e.installBuiltinTypes(runtime.NewEnvironment()); err != nil {
			return nil, err
		}
	}
	functions, ok := e.builtins[module]
	if !ok {
		return nil, fmt.Errorf("unknown builtin module %s", module)
	}
	function, ok := functions[name]
	if !ok {
		return nil, fmt.Errorf("unknown builtin function %s.%s", module, name)
	}
	call := &ast.CallExpression{
		Callee: &ast.SelectorExpression{
			Object: &ast.Identifier{Value: module},
			Name:   &ast.Identifier{Value: name},
		},
		Arguments: make([]ast.CallArgument, len(args)),
	}
	for i := range args {
		call.Arguments[i].Value = &ast.Literal{Value: nil}
		if i < len(argNames) && argNames[i] != "" {
			call.Arguments[i].Name = &ast.Identifier{Value: argNames[i]}
		}
	}
	return function(call, args)
}

func (e *Evaluator) childForCallback() *Evaluator {
	child := NewWithArgsAndModulePaths(e.stdout, e.args, e.modulePaths)
	child.stderr = e.stderr
	child.stdin = e.stdin
	child.stdinReader = e.stdinReader
	child.parent = e
	child.maxCallDepth = e.maxCallDepth
	child.imports = maps.Clone(e.imports)
	child.importNames = maps.Clone(e.importNames)
	child.modulePrograms = maps.Clone(e.modulePrograms)
	child.manifests = maps.Clone(e.manifests)
	child.typeAliases = maps.Clone(e.typeAliases)
	child.testClass = e.testClass
	child.httpRequestClass = e.httpRequestClass
	child.httpResponseClass = e.httpResponseClass
	child.httpClientClass = e.httpClientClass
	child.httpBuilderClass = e.httpBuilderClass
	child.httpCookieJarClass = e.httpCookieJarClass
	child.httpFetchStreamClass = e.httpFetchStreamClass
	child.processClass = e.processClass
	child.processResultClass = e.processResultClass
	child.dbConnectionClass = e.dbConnectionClass
	child.dbTransactionClass = e.dbTransactionClass
	child.dbStatementClass = e.dbStatementClass
	child.dbRowsClass = e.dbRowsClass
	child.streamIfaces = maps.Clone(e.streamIfaces)
	return child
}

func (e *Evaluator) Eval(program *ast.Program) (result Result, err error) {
	defer func() {
		cleanupErr := e.Cleanup()
		if err == nil && cleanupErr != nil {
			err = cleanupErr
		}
	}()
	env := runtime.NewEnvironment()
	if err := e.installBuiltinTypes(env); err != nil {
		return Result{}, err
	}
	e.pushDeferFrame()
	sig, err := e.evalStatements(program.Statements, env)
	if err != nil {
		return Result{}, err
	}
	sig, err = e.runAndPopDefers(sig)
	if err != nil {
		return Result{}, err
	}
	if sig.exited {
		return Result{ExitCode: sig.exitCode, Exited: true}, nil
	}
	if sig.kind == "throw" && sig.thrown != nil {
		msg := "uncaught " + sig.thrown.Inspect()
		if sig.thrown.StackTrace != "" {
			msg += sig.thrown.StackTrace
		}
		return Result{}, fmt.Errorf("%s", msg)
	}
	return Result{ExitCode: 0}, nil
}

// runDestructorSweep invokes the destructor of every tracked
// instance that hasn't already been destroyed via `del`. Instances
// are visited in reverse-creation order (LIFO) so younger objects -
// which may depend on older ones - clean up first. A destructor
// itself may instantiate new destructor-bearing objects; this is
// handled by draining the registry repeatedly, up to maxDepth
// rounds, to guard against accidental destructor recursion.
func (e *Evaluator) runDestructorSweep() {
	const maxDepth = 4
	for depth := 0; depth < maxDepth; depth++ {
		if len(e.destructibleInstances) == 0 {
			return
		}
		batch := e.destructibleInstances
		e.destructibleInstances = nil
		for i := len(batch) - 1; i >= 0; i-- {
			inst := batch[i]
			if inst == nil || inst.Destroyed || inst.Class == nil || inst.Class.Destructor == nil {
				continue
			}
			inst.Destroyed = true
			if _, err := e.applyFunctionWithThis(*inst.Class.Destructor, nil, inst); err != nil {
				fmt.Fprintf(e.stderr, "destructor for %s: %v\n", inst.Class.Name, err)
			}
		}
	}
}

func (e *Evaluator) Cleanup() error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// User-defined destructors run before native handle pools are
	// drained so destructors can still call io.close / db.close etc.
	e.runDestructorSweep()

	e.fileMu.Lock()
	files := e.files
	e.files = map[int64]*os.File{}
	e.fileMu.Unlock()
	for _, file := range files {
		record(file.Close())
	}

	e.streamMu.Lock()
	streams := e.streams
	e.streams = map[int64]*ioStreamHandle{}
	e.streamMu.Unlock()
	for _, stream := range streams {
		record(closeIOStreamHandle(stream))
	}

	e.watchMu.Lock()
	watches := e.watches
	e.watches = map[int64]*watchHandle{}
	e.watchMu.Unlock()
	for _, watcher := range watches {
		record(stopWatchHandle(watcher))
	}

	e.netServerMu.Lock()
	netServers := e.netServers
	e.netServers = map[int64]*netServerHandle{}
	e.netServerMu.Unlock()
	for _, server := range netServers {
		record(stopNetServerHandle(server))
	}

	e.sshMu.Lock()
	sshTunnels := e.sshTunnels
	sshSessions := e.sshSessions
	sshClients := e.sshClients
	e.sshTunnels = map[int64]*sshTunnelHandle{}
	e.sshSessions = map[int64]*sshSessionHandle{}
	e.sshClients = map[int64]*sshClientHandle{}
	e.sshMu.Unlock()
	for _, tunnel := range sshTunnels {
		record(stopSSHTunnelHandle(tunnel))
	}
	for _, session := range sshSessions {
		_ = session.session.Close()
	}
	for _, client := range sshClients {
		record(closeSSHClientHandle(client))
	}

	e.dbMu.Lock()
	dbRows := e.dbRows
	stmts := e.stmts
	txs := e.txs
	dbs := e.dbs
	e.dbRows = map[int64]*dbRowsHandle{}
	e.stmts = map[int64]*dbStmtHandle{}
	e.txs = map[int64]*dbTxHandle{}
	e.dbs = map[int64]*sql.DB{}
	e.dbDrivers = map[int64]string{}
	e.dbMu.Unlock()
	for _, rows := range dbRows {
		if !rows.closed {
			record(rows.rows.Close())
		}
	}
	for _, stmt := range stmts {
		record(stmt.stmt.Close())
	}
	for _, tx := range txs {
		record(tx.tx.Rollback())
	}
	for _, db := range dbs {
		record(db.Close())
	}

	e.netMu.Lock()
	netHandles := e.netHandles
	e.netHandles = map[int64]*netHandle{}
	e.netMu.Unlock()
	for _, handle := range netHandles {
		record(closeNetHandle(handle))
	}

	e.httpServerMu.Lock()
	httpServers := e.httpServers
	e.httpServers = map[int64]*httpServerHandle{}
	e.httpServerMu.Unlock()
	for _, handle := range httpServers {
		record(closeHTTPServerHandle(handle))
	}

	e.httpStreamMu.Lock()
	e.httpStreams = map[int64]*httpStreamHandle{}
	e.httpStreamMu.Unlock()

	e.wsMu.Lock()
	websockets := e.websockets
	e.websockets = map[int64]*wsHandle{}
	e.wsMu.Unlock()
	for _, h := range websockets {
		conn := h.conn
		record(conn.Close())
	}

	e.logMu.Lock()
	loggers := e.loggers
	e.loggers = map[int64]*loggerHandle{}
	e.logMu.Unlock()
	for _, logger := range loggers {
		if logger.closer != nil {
			record(logger.closer.Close())
		}
	}

	e.processMu.Lock()
	processes := e.processes
	e.processes = map[int64]*processHandle{}
	e.processMu.Unlock()
	for _, process := range processes {
		cleanupProcess(process)
	}

	e.traceMu.Lock()
	e.traces = map[int64]*traceSpan{}
	e.traceMu.Unlock()

	e.jsonMu.Lock()
	e.jsonReaders = map[int64]*jsonStreamReader{}
	e.jsonMu.Unlock()

	e.xmlMu.Lock()
	e.xmlReaders = map[int64]*xmlStreamReader{}
	e.xmlMu.Unlock()

	e.csvMu.Lock()
	e.csvReaders = map[int64]*csvStreamReader{}
	e.csvMu.Unlock()

	e.yamlMu.Lock()
	e.yamlReaders = map[int64]*yamlStreamReader{}
	e.yamlMu.Unlock()

	e.extMu.Lock()
	extConns := e.extConns
	e.extConns = map[int64]*extHandle{}
	e.extMu.Unlock()
	for _, h := range extConns {
		closeExtHandle(h)
	}

	return firstErr
}

func closeNetHandle(handle *netHandle) error {
	close := func(closer io.Closer) error {
		if err := closer.Close(); err != nil &&
			!errors.Is(err, os.ErrClosed) &&
			!errors.Is(err, net.ErrClosed) &&
			!strings.Contains(err.Error(), "use of closed network connection") {
			return err
		}
		return nil
	}
	if handle.listener != nil {
		return close(handle.listener)
	}
	if handle.conn != nil {
		return close(handle.conn)
	}
	if handle.packet != nil {
		return close(handle.packet)
	}
	return nil
}

func closeIOStreamHandle(handle *ioStreamHandle) error {
	if handle == nil || handle.closed {
		return nil
	}
	handle.closed = true
	handle.bufReader = nil
	if handle.restore != nil {
		handle.restore()
		handle.restore = nil
	}
	if handle.closer != nil {
		if err := handle.closer.Close(); err != nil &&
			!errors.Is(err, os.ErrClosed) &&
			!errors.Is(err, net.ErrClosed) &&
			!strings.Contains(err.Error(), "use of closed network connection") {
			return err
		}
	}
	return nil
}

// isIOStreamKind reports whether a NativeObject is one of the stream
// kinds io.* helpers operate on. Centralises the previously-repeated
// `kind == "IOStream" || kind == "IOCapture"` check so a typo in any
// one site can't silently skip the branch.
func isIOStreamKind(value runtime.Value) (runtime.NativeObject, bool) {
	obj, ok := value.(runtime.NativeObject)
	if !ok {
		return runtime.NativeObject{}, false
	}
	if obj.Kind != "IOStream" && obj.Kind != "IOCapture" {
		return runtime.NativeObject{}, false
	}
	return obj, true
}

func cleanupProcess(process *processHandle) {
	if process.cancel != nil {
		process.cancel()
	}
	if process.stdin != nil {
		_ = process.stdin.Close()
	}
	if process.stdout != nil {
		_ = process.stdout.Close()
	}
	if process.stderr != nil {
		_ = process.stderr.Close()
	}
	if process.cmd != nil && process.cmd.Process != nil && process.cmd.ProcessState == nil {
		_ = process.cmd.Process.Kill()
		_ = process.cmd.Wait()
	}
}

func (e *Evaluator) evalStatements(stmts []ast.Statement, env *runtime.Environment) (signal, error) {
	for _, stmt := range stmts {
		e.callDebugHook(stmt, env, "step")
		sig, err := e.evalStatement(stmt, env)
		if err != nil {
			var thrown thrownError
			if errors.As(err, &thrown) {
				errValue := e.withTrace(thrown.value)
				return signal{kind: "throw", thrown: &errValue}, nil
			}
			errValue := e.withTrace(runtime.NewRecoverableError(err))
			return signal{kind: "throw", thrown: &errValue}, nil
		}
		if sig.kind != "" || sig.exited {
			return sig, nil
		}
	}
	return signal{}, nil
}

func (e *Evaluator) evalImportStatement(stmt *ast.ImportStatement, env *runtime.Environment) (signal, error) {
	alias := stmt.ModuleName()
	if alias == "" {
		return signal{}, fmt.Errorf("empty import path")
	}
	canonical := strings.Join(stmt.Path, ".")
	if imported, ok := e.importNames[alias]; ok && imported == canonical {
		if _, exists := env.Get(alias); exists {
			return signal{}, nil
		}
	}
	module, err := e.resolveImportedModule(canonical, alias)
	if err != nil {
		return signal{}, err
	}
	if err := env.Define(alias, module, true); err != nil {
		if err := env.Assign(alias, module); err != nil {
			return signal{}, err
		}
	}
	e.imports[alias] = true
	e.importNames[alias] = canonical
	return signal{}, nil
}

// Stdlib wins externally; self-import falls through to native.
func (e *Evaluator) resolveImportedModule(canonical, alias string) (*runtime.Module, error) {
	_, nativeExists := e.builtins[canonical]
	if path, perr := e.resolveModulePath(canonical); perr == nil && !e.loading[path] {
		return e.loadUserModule(canonical, alias)
	}
	if nativeExists {
		return e.builtinModuleValue(canonical, alias), nil
	}
	return e.loadUserModule(canonical, alias)
}

func (e *Evaluator) evalFromImportStatement(stmt *ast.FromImportStatement, env *runtime.Environment) (signal, error) {
	canonical := strings.Join(stmt.Path, ".")
	if canonical == "" {
		return signal{}, fmt.Errorf("empty import path")
	}
	preferUser := false
	if path, perr := e.resolveModulePath(canonical); perr == nil && !e.loading[path] {
		preferUser = true
	}
	if !preferUser {
		if _, ok := e.builtins[canonical]; ok {
			moduleClasses := e.builtinModuleValue(canonical, "").Exports
			functions := e.builtins[canonical]
			for _, item := range stmt.Names {
				if item.Name == nil {
					continue
				}
				name := item.Name.Value
				local := item.Local()
				value, ok := e.resolveBuiltinExport(moduleClasses, functions, canonical, name)
				if !ok {
					return signal{}, fmt.Errorf("from %s import %s: %s is not exported", canonical, name, name)
				}
				if err := env.DefineImported(local, value, canonical+"."+name); err != nil {
					return signal{}, err
				}
			}
			e.imports[canonical] = true
			return signal{}, nil
		}
	}
	module, err := e.loadUserModule(canonical, "")
	if err != nil {
		return signal{}, err
	}
	for _, item := range stmt.Names {
		if item.Name == nil {
			continue
		}
		name := item.Name.Value
		local := item.Local()
		value, ok := module.Exports[name]
		if !ok {
			if _, hasNative := e.builtins[canonical]; hasNative {
				if v, found := e.resolveBuiltinExport(e.builtinModuleValue(canonical, "").Exports, e.builtins[canonical], canonical, name); found {
					if err := env.DefineImported(local, v, canonical+"."+name); err != nil {
						return signal{}, err
					}
					continue
				}
			}
			return signal{}, fmt.Errorf("from %s import %s: %s is not exported", canonical, name, name)
		}
		if err := env.DefineImported(local, value, canonical+"."+name); err != nil {
			return signal{}, err
		}
	}
	return signal{}, nil
}

// resolveBuiltinExport finds a named symbol on a native module: a
// class registered in builtinModuleValue's Exports, or a registry
// function wrapped as a callable runtime.Function value.
func (e *Evaluator) resolveBuiltinExport(classes map[string]runtime.Value, functions map[string]builtinFunc, canonical, name string) (runtime.Value, bool) {
	if value, ok := classes[name]; ok {
		return value, true
	}
	if fn, ok := functions[name]; ok {
		return e.wrapBuiltinAsFunction(canonical, name, fn), true
	}
	return nil, false
}

func (e *Evaluator) wrapBuiltinAsFunction(canonical, name string, fn builtinFunc) runtime.Function {
	syntheticCall := &ast.CallExpression{
		Callee: &ast.SelectorExpression{
			Object: &ast.Identifier{Value: canonical},
			Name:   &ast.Identifier{Value: name},
		},
	}
	return runtime.Function{
		Name: canonical + "." + name,
		Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			return fn(syntheticCall, args)
		},
	}
}

// BuiltinModule lets the bytecode loader fall back to native on self-import.
func (e *Evaluator) BuiltinModule(canonical, alias string) *runtime.Module {
	if _, ok := e.builtins[canonical]; !ok {
		return nil
	}
	return e.builtinModuleValue(canonical, alias)
}

func (e *Evaluator) builtinModuleValue(canonical, alias string) *runtime.Module {
	exports := map[string]runtime.Value{}
	switch canonical {
	case "http":
		if e.httpRequestClass != nil {
			exports["Request"] = e.httpRequestClass
		}
		if e.httpResponseClass != nil {
			exports["Response"] = e.httpResponseClass
		}
		if e.httpClientClass != nil {
			exports["Client"] = e.httpClientClass
		}
		if e.httpBuilderClass != nil {
			exports["Builder"] = e.httpBuilderClass
		}
		if e.httpCookieJarClass != nil {
			exports["CookieJar"] = e.httpCookieJarClass
		}
		if e.httpFetchStreamClass != nil {
			exports["FetchStream"] = e.httpFetchStreamClass
		}
	case "process":
		if e.processClass != nil {
			exports["Process"] = e.processClass
		}
		if e.processResultClass != nil {
			exports["Result"] = e.processResultClass
		}
	case "db":
		if e.dbConnectionClass != nil {
			exports["Connection"] = e.dbConnectionClass
		}
		if e.dbTransactionClass != nil {
			exports["Transaction"] = e.dbTransactionClass
		}
		if e.dbStatementClass != nil {
			exports["Statement"] = e.dbStatementClass
		}
		if e.dbRowsClass != nil {
			exports["Rows"] = e.dbRowsClass
		}
	case "test":
		if e.testClass != nil {
			exports["Test"] = e.testClass
		}
	case "json":
		e.addStreamInterfaceExport(exports, "JsonStreamInterface")
	case "xml":
		e.addStreamInterfaceExport(exports, "XmlStreamInterface")
	case "yaml":
		e.addStreamInterfaceExport(exports, "YamlStreamInterface")
	case "csv":
		e.addStreamInterfaceExport(exports, "CsvStreamInterface")
	case "log":
		e.addStreamInterfaceExport(exports, "LogInterface")
	}
	return &runtime.Module{Name: alias, Canonical: canonical, Exports: exports}
}

func (e *Evaluator) addStreamInterfaceExport(exports map[string]runtime.Value, name string) {
	if e.streamIfaces == nil {
		return
	}
	if iface, ok := e.streamIfaces[strings.ToLower(name)]; ok {
		exports[name] = iface
	}
}

func (e *Evaluator) loadUserModule(canonical, alias string) (*runtime.Module, error) {
	if module, ok := e.modules[canonical]; ok {
		return module, nil
	}
	path, err := e.resolveModulePath(canonical)
	if err != nil {
		return nil, err
	}
	if e.loading[path] {
		return nil, fmt.Errorf("circular import detected for %s", canonical)
	}
	e.loading[path] = true
	defer delete(e.loading, path)

	program, err := e.parseAnalyzedModule(canonical, path)
	if err != nil {
		return nil, err
	}

	moduleEnv := runtime.NewEnvironment()
	previousPaths := e.modulePaths
	moduleDir := filepath.Dir(path)
	e.modulePaths = append([]string{moduleDir}, e.modulePaths...)
	sig, err := e.evalStatements(program.Statements, moduleEnv)
	e.modulePaths = previousPaths
	if err != nil {
		return nil, fmt.Errorf("evaluate module %s: %w", canonical, err)
	}
	if sig.kind != "" || sig.exited {
		return nil, fmt.Errorf("module %s cannot return, throw, break, continue, or exit during import", canonical)
	}
	exports, err := exportedValues(program, moduleEnv)
	if err != nil {
		return nil, fmt.Errorf("export module %s: %w", canonical, err)
	}
	module := &runtime.Module{Name: alias, Canonical: canonical, Exports: exports}
	e.modules[canonical] = module
	return module, nil
}

func (e *Evaluator) parseAnalyzedModule(canonical string, path string) (*ast.Program, error) {
	if program, ok := e.modulePrograms[path]; ok {
		return program, nil
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read module %s: %w", canonical, err)
	}
	p := parser.New(lexer.New(string(source)))
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("parse module %s: %s", canonical, strings.Join(p.Errors(), "\n"))
	}
	if diagnostics := semantic.New().Analyze(program); len(diagnostics) > 0 {
		errorMessages := make([]string, 0, len(diagnostics))
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == semantic.SeverityWarning {
				fmt.Fprintf(e.stderr, "warning: module %s: %s\n", canonical, diagnostic.Message)
				continue
			}
			errorMessages = append(errorMessages, diagnostic.Message)
		}
		if len(errorMessages) > 0 {
			return nil, fmt.Errorf("analyze module %s: %s", canonical, strings.Join(errorMessages, "\n"))
		}
	}
	e.modulePrograms[path] = program
	return program, nil
}

func (e *Evaluator) resolveModulePath(canonical string) (string, error) {
	resolver := modules.NewResolver(e.modulePaths)
	return resolver.Resolve(canonical)
}

type packageModuleRoot struct {
	path     string
	manifest *packageManifest
}

func (e *Evaluator) moduleSearchPaths() []string {
	paths := append([]string(nil), e.modulePaths...)
	if env := os.Getenv("GEBLANG_PATH"); env != "" {
		paths = append(paths, filepath.SplitList(env)...)
	}
	return paths
}

func (e *Evaluator) packageModuleRoots() ([]packageModuleRoot, error) {
	roots := []packageModuleRoot{}
	seenRoots := map[string]bool{}
	seenManifests := map[string]bool{}
	for _, base := range e.moduleSearchPaths() {
		if base == "" {
			base = "."
		}
		manifest, err := e.findPackageManifest(base)
		if err != nil {
			return nil, err
		}
		if manifest == nil {
			continue
		}
		if err := e.collectPackageModuleRoots(manifest, seenManifests, seenRoots, &roots); err != nil {
			return nil, err
		}
	}
	return roots, nil
}

func (e *Evaluator) collectPackageModuleRoots(manifest *packageManifest, seenManifests map[string]bool, seenRoots map[string]bool, roots *[]packageModuleRoot) error {
	if manifest == nil {
		return nil
	}
	if seenManifests[manifest.Path] {
		return nil
	}
	seenManifests[manifest.Path] = true
	for _, moduleRoot := range manifest.moduleRoots() {
		if seenRoots[moduleRoot] {
			continue
		}
		seenRoots[moduleRoot] = true
		*roots = append(*roots, packageModuleRoot{path: moduleRoot, manifest: manifest})
	}
	for name, dependency := range manifest.Dependencies {
		if dependency.Path == "" {
			return fmt.Errorf("package %s dependency %s has no path", manifestName(manifest), name)
		}
		dependencyRoot := filepath.Clean(filepath.Join(manifest.Root, dependency.Path))
		dependencyManifest, err := e.findPackageManifest(dependencyRoot)
		if err != nil {
			return err
		}
		if dependencyManifest == nil {
			dependencyManifest = &packageManifest{
				Path:         filepath.Clean(filepath.Join(dependencyRoot, "geblang.yaml")),
				Root:         dependencyRoot,
				Name:         name,
				Dependencies: map[string]packageDependency{},
			}
		}
		if err := e.collectPackageModuleRoots(dependencyManifest, seenManifests, seenRoots, roots); err != nil {
			return err
		}
	}
	return nil
}

func (e *Evaluator) findPackageManifest(start string) (*packageManifest, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		current = filepath.Clean(start)
	}
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		for _, name := range []string{"geblang.yaml", "geblang.yml", "geblang.json"} {
			path := filepath.Join(current, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return e.loadPackageManifest(path)
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil, nil
		}
		current = parent
	}
}

func (e *Evaluator) loadPackageManifest(path string) (*packageManifest, error) {
	path = filepath.Clean(path)
	if manifest, ok := e.manifests[path]; ok {
		return manifest, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var parsed packageManifestFile
	if err := yamllib.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse package manifest %s: %w", path, err)
	}
	name := parsed.Name
	if name == "" {
		name = parsed.Package.Name
	}
	version := parsed.Version
	if version == "" {
		version = parsed.Package.Version
	}
	paths := append([]string(nil), parsed.Paths...)
	paths = append(paths, parsed.ModulePaths...)
	manifest := &packageManifest{
		Path:         path,
		Root:         filepath.Dir(path),
		Name:         name,
		Version:      version,
		Source:       parsed.Source,
		Paths:        paths,
		Dependencies: parsed.Dependencies,
		Extensions:   parsed.Extensions,
		Permissions:  parsed.Permissions,
	}
	if manifest.Dependencies == nil {
		manifest.Dependencies = map[string]packageDependency{}
	}
	if manifest.Extensions == nil {
		manifest.Extensions = map[string]*extConfig{}
	}
	e.manifests[path] = manifest
	return manifest, nil
}

func manifestName(manifest *packageManifest) string {
	if manifest.Name != "" {
		return manifest.Name
	}
	return manifest.Root
}

func (m *packageManifest) moduleRoots() []string {
	roots := []string{}
	if m.Source != "" {
		roots = append(roots, filepath.Clean(filepath.Join(m.Root, m.Source)))
	} else {
		roots = append(roots, m.Root)
	}
	for _, path := range m.Paths {
		if path == "" {
			continue
		}
		roots = append(roots, filepath.Clean(filepath.Join(m.Root, path)))
	}
	return roots
}

func packageRelativeModuleBases(canonical, packageName string) []string {
	bases := []string{filepath.Join(strings.Split(canonical, ".")...)}
	if packageName == "" {
		return bases
	}
	if canonical == packageName {
		return append(bases, "init")
	}
	prefix := packageName + "."
	if strings.HasPrefix(canonical, prefix) {
		stripped := strings.TrimPrefix(canonical, prefix)
		bases = append(bases, filepath.Join(strings.Split(stripped, ".")...))
	}
	return bases
}

func exportedValues(program *ast.Program, env *runtime.Environment) (map[string]runtime.Value, error) {
	exports := map[string]runtime.Value{}
	for _, stmt := range program.Statements {
		exportStmt, ok := stmt.(*ast.ExportStatement)
		if !ok {
			continue
		}
		name := exportedStatementName(exportStmt.Statement)
		if name == "" {
			return nil, fmt.Errorf("unsupported export %T", exportStmt.Statement)
		}
		value, ok := env.Get(name)
		if !ok {
			return nil, fmt.Errorf("export %q was not declared", name)
		}
		exports[name] = value
	}
	return exports, nil
}

func exportedStatementName(stmt ast.Statement) string {
	switch stmt := stmt.(type) {
	case *ast.DeclarationStatement:
		return stmt.Name.Value
	case *ast.FunctionStatement:
		return stmt.Name.Value
	case *ast.ClassStatement:
		return stmt.Name.Value
	case *ast.InterfaceStatement:
		return stmt.Name.Value
	default:
		return ""
	}
}

func (e *Evaluator) applyCallableFunctionDecorators(fn runtime.Function, decorators []ast.Decorator, env *runtime.Environment) (runtime.Function, error) {
	current := fn
	for i := len(decorators) - 1; i >= 0; i-- {
		decorator := decorators[i]
		if decorator.Name == nil {
			continue
		}
		value, ok := env.Get(decorator.Name.Value)
		if !ok {
			continue
		}
		args, err := e.decoratorCallArguments(current, decorator, env)
		if err != nil {
			return runtime.Function{}, err
		}
		var result runtime.Value
		switch callable := value.(type) {
		case runtime.Function:
			bound, ok := bindEvaluatedFunctionCallArguments(callable, args)
			if !ok || !functionArgumentsMatch(callable, bound) {
				return runtime.Function{}, fmt.Errorf("decorator %s cannot be called with decorated function arguments", decorator.Name.Value)
			}
			result, err = e.applyFunction(callable, bound)
		case runtime.OverloadedFunction:
			var matches []runtime.Function
			var matchedArgs [][]runtime.Value
			for _, overload := range callable.Overloads {
				bound, ok := bindEvaluatedFunctionCallArguments(overload, args)
				if !ok || !functionArgumentsMatch(overload, bound) {
					continue
				}
				matches = append(matches, overload)
				matchedArgs = append(matchedArgs, bound)
			}
			if len(matches) == 0 {
				return runtime.Function{}, fmt.Errorf("no matching overload for decorator %s", decorator.Name.Value)
			}
			if len(matches) > 1 {
				return runtime.Function{}, fmt.Errorf("ambiguous overload for decorator %s", decorator.Name.Value)
			}
			result, err = e.applyFunction(matches[0], matchedArgs[0])
		default:
			continue
		}
		if err != nil {
			return runtime.Function{}, err
		}
		next, ok := result.(runtime.Function)
		if !ok {
			return runtime.Function{}, fmt.Errorf("decorator %s must return function, got %s", decorator.Name.Value, result.TypeName())
		}
		if !decoratorWrapperCompatible(fn, next) {
			return runtime.Function{}, fmt.Errorf("decorator %s returned incompatible wrapper for %s", decorator.Name.Value, fn.Name)
		}
		current = mergeDecoratedFunctionMetadata(fn, next)
	}
	return current, nil
}

func decoratorWrapperCompatible(original runtime.Function, wrapper runtime.Function) bool {
	origMin, origMax, origVariadic := functionArityRange(original.Parameters)
	wrapMin, wrapMax, wrapVariadic := functionArityRange(wrapper.Parameters)
	if wrapMin > origMin {
		return false
	}
	if origVariadic {
		return wrapVariadic
	}
	return wrapVariadic || wrapMax >= origMax
}

func functionArityRange(params []ast.Parameter) (int, int, bool) {
	min := len(params)
	for min > 0 && params[min-1].Default != nil {
		min--
	}
	variadic := len(params) > 0 && params[len(params)-1].Variadic
	if variadic {
		if min == len(params) {
			min--
		}
		return min, len(params), true
	}
	return min, len(params), false
}

func (e *Evaluator) decoratorCallArguments(fn runtime.Function, decorator ast.Decorator, env *runtime.Environment) ([]evaluatedCallArg, error) {
	args := []evaluatedCallArg{{value: fn}}
	for _, arg := range decorator.Arguments {
		value, err := e.evalExpression(arg.Value, env)
		if err != nil {
			return nil, fmt.Errorf("decorator %s argument: %w", decorator.Name.Value, err)
		}
		if arg.Spread {
			list, ok := value.(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("decorator %s spread argument must be a list", decorator.Name.Value)
			}
			for _, element := range list.Elements {
				args = append(args, evaluatedCallArg{value: element})
			}
			continue
		}
		name := ""
		if arg.Name != nil {
			name = arg.Name.Value
		}
		args = append(args, evaluatedCallArg{name: name, value: value})
	}
	return args, nil
}

func mergeDecoratedFunctionMetadata(original runtime.Function, decorated runtime.Function) runtime.Function {
	if decorated.Name == "" {
		decorated.Name = original.Name
	}
	if decorated.Doc == "" {
		decorated.Doc = original.Doc
	}
	if decorated.Target == "" {
		decorated.Target = original.Target
	}
	if len(decorated.Decorators) == 0 {
		decorated.Decorators = append([]ast.Decorator(nil), original.Decorators...)
	}
	if len(decorated.TypeParameters) == 0 {
		decorated.TypeParameters = append([]string(nil), original.TypeParameters...)
	}
	if !decorated.ForwardThis {
		decorated.ForwardThis = original.ForwardThis
	}
	return decorated
}

func (e *Evaluator) applyCallableClassDecorators(class *runtime.Class, decorators []ast.Decorator, env *runtime.Environment) (runtime.Value, error) {
	var current runtime.Value = class
	for i := len(decorators) - 1; i >= 0; i-- {
		decorator := decorators[i]
		if decorator.Name == nil {
			continue
		}
		value, ok := env.Get(decorator.Name.Value)
		if !ok {
			continue
		}
		args, err := e.decoratorClassCallArguments(current, decorator, env)
		if err != nil {
			return nil, err
		}
		var result runtime.Value
		switch callable := value.(type) {
		case runtime.Function:
			bound, ok := bindEvaluatedFunctionCallArguments(callable, args)
			if !ok || !functionArgumentsMatch(callable, bound) {
				return nil, fmt.Errorf("decorator %s cannot be called with decorated class arguments", decorator.Name.Value)
			}
			result, err = e.applyFunction(callable, bound)
		case runtime.OverloadedFunction:
			var matches []runtime.Function
			var matchedArgs [][]runtime.Value
			for _, overload := range callable.Overloads {
				bound, ok := bindEvaluatedFunctionCallArguments(overload, args)
				if !ok || !functionArgumentsMatch(overload, bound) {
					continue
				}
				matches = append(matches, overload)
				matchedArgs = append(matchedArgs, bound)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no matching overload for decorator %s", decorator.Name.Value)
			}
			if len(matches) > 1 {
				return nil, fmt.Errorf("ambiguous overload for decorator %s", decorator.Name.Value)
			}
			result, err = e.applyFunction(matches[0], matchedArgs[0])
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		switch next := result.(type) {
		case *runtime.Class:
			current = mergeDecoratedClassMetadata(class, next)
		case runtime.Function, runtime.OverloadedFunction:
			current = result
		default:
			return nil, fmt.Errorf("decorator %s must return class or callable, got %s", decorator.Name.Value, result.TypeName())
		}
	}
	return current, nil
}

func (e *Evaluator) decoratorClassCallArguments(class runtime.Value, decorator ast.Decorator, env *runtime.Environment) ([]evaluatedCallArg, error) {
	args := []evaluatedCallArg{{value: class}}
	for _, arg := range decorator.Arguments {
		value, err := e.evalExpression(arg.Value, env)
		if err != nil {
			return nil, fmt.Errorf("decorator %s argument: %w", decorator.Name.Value, err)
		}
		if arg.Spread {
			list, ok := value.(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("decorator %s spread argument must be a list", decorator.Name.Value)
			}
			for _, element := range list.Elements {
				args = append(args, evaluatedCallArg{value: element})
			}
			continue
		}
		name := ""
		if arg.Name != nil {
			name = arg.Name.Value
		}
		args = append(args, evaluatedCallArg{name: name, value: value})
	}
	return args, nil
}

func mergeDecoratedClassMetadata(original *runtime.Class, decorated *runtime.Class) *runtime.Class {
	if decorated.Name == "" {
		decorated.Name = original.Name
	}
	if decorated.Doc == "" {
		decorated.Doc = original.Doc
	}
	if len(decorated.Decorators) == 0 {
		decorated.Decorators = original.Decorators
	}
	if len(decorated.TypeParameters) == 0 {
		decorated.TypeParameters = append([]string(nil), original.TypeParameters...)
	}
	if decorated.Module == "" {
		decorated.Module = original.Module
	}
	if decorated.Env == nil {
		decorated.Env = original.Env
	}
	return decorated
}

func (e *Evaluator) evalStatement(stmt ast.Statement, env *runtime.Environment) (signal, error) {
	switch stmt := stmt.(type) {
	case *ast.ModuleStatement:
		return signal{}, nil
	case *ast.ImportStatement:
		return e.evalImportStatement(stmt, env)
	case *ast.FromImportStatement:
		return e.evalFromImportStatement(stmt, env)
	case *ast.ExportStatement:
		return e.evalStatement(stmt.Statement, env)
	case *ast.InitStatement:
		return e.evalBlock(stmt.Body, env)
	case *ast.TypeAliasStatement:
		e.typeAliases[strings.ToLower(stmt.Name.Value)] = e.resolveTypeRef(stmt.Type)
		return signal{}, nil
	case *ast.DeclarationStatement:
		value := runtime.Value(runtime.Null{})
		expectedType := e.resolveTypeRef(stmt.Type)
		if stmt.Value != nil {
			evaluated, err := e.evalExpressionWithExpectedType(stmt.Value, env, expectedType)
			if err != nil {
				return signal{}, err
			}
			value = evaluated
			// Only enforce generic collection element types on declarations (e.g. list<int>, int[]).
			// Scalar type coercions (float/decimal, int widening, etc.) are handled elsewhere.
			if expectedType != nil && expectedType.Operator == "" {
				isListAlias := expectedType.ListAlias && len(expectedType.Arguments) == 0
				hasGenericArgs := len(expectedType.Arguments) > 0
				if hasGenericArgs || isListAlias {
					typeName := strings.ToLower(simpleTypeName(expectedType.Name))
					if typeName == "list" || expectedType.ListAlias || typeName == "set" || typeName == "dict" {
						if !matchValueToTypeRef(nil, value, expectedType) {
							suffix := collectionMismatchSuffix(value, expectedType)
							// When a suffix identifies the specific bad element, use the base type
							// name to avoid a misleading "list<int> to list<int>" message.
							gotName := descriptiveTypeName(value)
							if suffix != "" {
								gotName = value.TypeName()
							}
							return signal{}, fmt.Errorf("type error: cannot assign %s to %s%s", gotName, expectedType.String(), suffix)
						}
						// Attach the reified element-type tag so subsequent
						// reflect.typeBindings() and `instanceof list<T>`
						// checks see the declared bindings rather than a
						// bare `list`. Untagged collections retain their
						// nil ElementTypes.
						value = tagCollectionWithTypeRef(value, expectedType)
					}
				}
			}
		}
		return signal{}, env.Define(stmt.Name.Value, value, stmt.Kind == "const" || stmt.Kind == "static const")
	case *ast.DestructuringStatement:
		return e.evalDestructuringStatement(stmt, env)
	case *ast.FunctionStatement:
		if stmt.Static {
			return signal{}, fmt.Errorf("static functions are parsed but not evaluated yet")
		}
		fn := runtime.Function{Name: stmt.Name.Value, Doc: stmt.Doc, TypeParameters: typeParameterNames(stmt.Generics), TypeParamConstraints: typeParamConstraints(stmt.Generics), Parameters: e.resolveParameters(stmt.Parameters), ReturnType: e.resolveTypeRef(stmt.ReturnType), Body: stmt.Body, Env: env, Decorators: stmt.Decorators, Target: "function", Async: stmt.Async, IsGenerator: blockContainsYield(stmt.Body), DefinitionLine: stmt.Token.Line, DefinitionColumn: stmt.Token.Column}
		decorated, err := e.applyCallableFunctionDecorators(fn, stmt.Decorators, env)
		if err != nil {
			return signal{}, err
		}
		fn = decorated
		return signal{}, env.DefineFunction(stmt.Name.Value, fn)
	case *ast.ExpressionStatement:
		if stmt.Expression == nil {
			return signal{}, nil
		}
		value, err := e.evalExpression(stmt.Expression, env)
		if err != nil {
			return signal{}, err
		}
		if exit, ok := value.(exitValue); ok {
			return signal{exited: true, exitCode: exit.code}, nil
		}
		return signal{}, nil
	case *ast.IfStatement:
		condition, err := e.evalBoolCondition(stmt.Condition, env)
		if err != nil {
			return signal{}, err
		}
		if condition {
			return e.evalBlock(stmt.Consequence, env)
		}
		for _, elseif := range stmt.ElseIfs {
			condition, err := e.evalBoolCondition(elseif.Condition, env)
			if err != nil {
				return signal{}, err
			}
			if condition {
				return e.evalBlock(elseif.Body, env)
			}
		}
		if stmt.Alternative != nil {
			return e.evalBlock(stmt.Alternative, env)
		}
		return signal{}, nil
	case *ast.WhileStatement:
		for {
			condition, err := e.evalBoolCondition(stmt.Condition, env)
			if err != nil {
				return signal{}, err
			}
			if !condition {
				return signal{}, nil
			}
			sig, err := e.evalBlock(stmt.Body, env)
			if err != nil {
				return signal{}, err
			}
			switch sig.kind {
			case "break":
				return signal{}, nil
			case "continue":
				continue
			case "":
			default:
				return sig, nil
			}
			if sig.exited {
				return sig, nil
			}
		}
	case *ast.ForStatement:
		return e.evalForStatement(stmt, env)
	case *ast.SimpleStatement:
		switch stmt.Kind {
		case "break", "continue":
			return signal{kind: stmt.Kind}, nil
		case "defer":
			e.registerDefer(stmt.Value, env)
			return signal{}, nil
		case "throw":
			value, err := e.evalExpression(stmt.Value, env)
			if err != nil {
				return signal{}, err
			}
			errValue, ok := value.(runtime.Error)
			if !ok {
				return signal{}, fmt.Errorf("throw expects Error, got %s", value.TypeName())
			}
			errValue = e.withTrace(errValue)
			return signal{kind: "throw", thrown: &errValue}, nil
		default:
			return signal{}, fmt.Errorf("unsupported simple statement %q", stmt.Kind)
		}
	case *ast.ReturnStatement:
		value := runtime.Value(runtime.Null{})
		if stmt.Value != nil {
			var expected *ast.TypeRef
			if fn := e.currentFunction(); fn != nil {
				expected = fn.ReturnType
			}
			evaluated, err := e.evalExpressionWithExpectedType(stmt.Value, env, expected)
			if err != nil {
				return signal{}, err
			}
			value = evaluated
		}
		return signal{kind: "return", value: value}, nil
	case *ast.YieldStatement:
		value := runtime.Value(runtime.Null{})
		if stmt.Value != nil {
			evaluated, err := e.evalExpression(stmt.Value, env)
			if err != nil {
				return signal{}, err
			}
			value = evaluated
		}
		closed, err := e.appendYield(value)
		if err != nil {
			return signal{}, err
		}
		if closed {
			return signal{kind: "generatorClosed"}, nil
		}
		return signal{}, nil
	case *ast.TryStatement:
		return e.evalTryStatement(stmt, env)
	case *ast.WithStatement:
		return e.evalWithStatement(stmt, env)
	case *ast.DelStatement:
		return signal{}, e.evalDelStatement(stmt, env)
	case *ast.MatchStatement:
		return e.evalMatchStatement(stmt, env)
	case *ast.SelectStatement:
		return e.evalSelectStatement(stmt, env)
	case *ast.ClassStatement:
		class, err := e.buildClass(stmt, env)
		if err != nil {
			return signal{}, err
		}
		callableDecorators := make([]ast.Decorator, 0, len(stmt.Decorators))
		for _, dec := range stmt.Decorators {
			if dec.Name.Value == "immutable" && len(dec.Arguments) == 0 {
				class.Immutable = true
			} else {
				callableDecorators = append(callableDecorators, dec)
			}
		}
		decorated, err := e.applyCallableClassDecorators(class, callableDecorators, env)
		if err != nil {
			return signal{}, err
		}
		if decoratedClass, ok := decorated.(*runtime.Class); ok {
			class = decoratedClass
		} else {
			e.decoratedClassIdents[stmt.Name.Value] = class.Name
		}
		e.registerGlobalClass(class)
		return signal{}, env.Define(stmt.Name.Value, decorated, true)
	case *ast.InterfaceStatement:
		iface, err := e.buildInterface(stmt, env)
		if err != nil {
			return signal{}, err
		}
		return signal{}, env.Define(stmt.Name.Value, iface, true)
	case *ast.EnumStatement:
		return signal{}, env.Define(stmt.Name.Value, buildEnum(stmt), true)
	default:
		return signal{}, fmt.Errorf("unsupported statement %T", stmt)
	}
}

func (e *Evaluator) evalBlock(block *ast.BlockStatement, outer *runtime.Environment) (signal, error) {
	if block == nil {
		return signal{}, nil
	}
	return e.evalStatements(block.Statements, runtime.NewEnclosedEnvironment(outer))
}

func (e *Evaluator) evalExpression(expr ast.Expression, env *runtime.Environment) (runtime.Value, error) {
	return e.evalExpressionWithExpectedType(expr, env, nil)
}

func (e *Evaluator) evalExpressionWithExpectedType(expr ast.Expression, env *runtime.Environment, expected *ast.TypeRef) (runtime.Value, error) {
	switch expr := expr.(type) {
	case *ast.Identifier:
		if value, ok := env.Get(expr.Value); ok {
			return value, nil
		}
		if runtime.IsBuiltinTypeName(expr.Value) {
			return runtime.Type{Name: strings.ToLower(expr.Value)}, nil
		}
		return nil, fmt.Errorf("%q is not declared", expr.Value)
	case *ast.IntegerLiteral:
		value, err := runtime.NewIntLiteral(expr.Value)
		if err != nil {
			return nil, err
		}
		if expected != nil && expected.Operator == "" {
			switch expected.Name {
			case "decimal":
				return intToDecimal(value), nil
			case "float":
				f, _ := new(big.Rat).SetInt(value.Value).Float64()
				return runtime.Float{Value: f}, nil
			}
		}
		return value, nil
	case *ast.DecimalLiteral:
		return runtime.NewDecimalLiteral(expr.Value)
	case *ast.FloatLiteral:
		stripped := strings.ReplaceAll(expr.Value[:len(expr.Value)-1], "_", "")
		value, err := strconv.ParseFloat(stripped, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float literal %q", expr.Value)
		}
		return runtime.Float{Value: value}, nil
	case *ast.StringLiteral:
		return runtime.String{Value: expr.Value}, nil
	case *ast.InterpolatedString:
		var sb strings.Builder
		for _, part := range expr.Parts {
			val, err := e.evalExpression(part, env)
			if err != nil {
				return nil, err
			}
			if s, ok := val.(runtime.String); ok {
				sb.WriteString(s.Value)
			} else {
				sb.WriteString(val.Inspect())
			}
		}
		return runtime.String{Value: sb.String()}, nil
	case *ast.FormattedInterpolation:
		val, err := e.evalExpression(expr.Value, env)
		if err != nil {
			return nil, err
		}
		formatted, err := native.FormatValueWithSpec(val, expr.Spec)
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: formatted}, nil
	case *ast.Literal:
		switch v := expr.Value.(type) {
		case bool:
			return runtime.Bool{Value: v}, nil
		case nil:
			return runtime.Null{}, nil
		default:
			return nil, fmt.Errorf("unsupported literal %v", expr.Value)
		}
	case *ast.ListLiteral:
		elements := make([]runtime.Value, 0, len(expr.Elements))
		for _, element := range expr.Elements {
			if spread, ok := element.(*ast.SpreadExpression); ok {
				value, err := e.evalExpression(spread.Value, env)
				if err != nil {
					return nil, err
				}
				list, ok := value.(*runtime.List)
				if !ok {
					return nil, fmt.Errorf("spread element must be a list")
				}
				elements = append(elements, list.Elements...)
				continue
			}
			value, err := e.evalExpression(element, env)
			if err != nil {
				return nil, err
			}
			elements = append(elements, value)
		}
		return &runtime.List{Elements: elements}, nil
	case *ast.DictLiteral:
		d := runtime.NewDict()
		for _, entry := range expr.Entries {
			if entry.Spread {
				src, err := e.evalExpression(entry.Value, env)
				if err != nil {
					return nil, err
				}
				srcDict, ok := src.(runtime.Dict)
				if !ok {
					return nil, fmt.Errorf("dict literal spread source must be dict, got %s", src.TypeName())
				}
				for _, k := range srcDict.OrderedKeys() {
					srcEntry := srcDict.Entries[k]
					d.PutEntry(k, runtime.DictEntry{Key: srcEntry.Key, Value: srcEntry.Value})
				}
				continue
			}
			key, err := e.evalExpression(entry.Key, env)
			if err != nil {
				return nil, err
			}
			value, err := e.evalExpression(entry.Value, env)
			if err != nil {
				return nil, err
			}
			d.PutEntry(dictKey(key), runtime.DictEntry{Key: key, Value: value})
		}
		return d, nil
	case *ast.SetLiteral:
		elements := map[string]runtime.SetEntry{}
		for _, element := range expr.Elements {
			if spread, ok := element.(*ast.SpreadExpression); ok {
				src, err := e.evalExpression(spread.Value, env)
				if err != nil {
					return nil, err
				}
				switch s := src.(type) {
				case runtime.Set:
					for k, entry := range s.Elements {
						elements[k] = entry
					}
				case *runtime.List:
					for _, v := range s.Elements {
						elements[dictKey(v)] = runtime.SetEntry{Value: v}
					}
				default:
					return nil, fmt.Errorf("set literal spread source must be set or list, got %s", src.TypeName())
				}
				continue
			}
			value, err := e.evalExpression(element, env)
			if err != nil {
				return nil, err
			}
			elements[dictKey(value)] = runtime.SetEntry{Value: value}
		}
		return runtime.Set{Elements: elements}, nil
	case *ast.RangeExpression:
		return e.evalRangeExpression(expr, env)
	case *ast.PrefixExpression:
		right, err := e.evalExpression(expr.Right, env)
		if err != nil {
			return nil, err
		}
		return e.evalPrefix(expr.Operator, right)
	case *ast.PostfixExpression:
		return e.evalIncrement(expr.Left, expr.Operator, env)
	case *ast.InfixExpression:
		if expr.Operator == "instanceof" {
			left, err := e.evalExpression(expr.Left, env)
			if err != nil {
				return nil, err
			}
			typeName, err := typeNameFromExpression(expr.Right)
			if err != nil {
				return nil, err
			}
			if bound, ok := env.GetTypeBinding(typeName); ok {
				typeName = bound
			}
			return runtime.Bool{Value: e.valueMatchesType(left, typeName)}, nil
		}
		if expr.Operator == "&&" || expr.Operator == "||" {
			left, err := e.evalExpression(expr.Left, env)
			if err != nil {
				return nil, err
			}
			leftBool, ok := left.(runtime.Bool)
			if !ok {
				return nil, fmt.Errorf("%s expects bool operands", expr.Operator)
			}
			if expr.Operator == "&&" && !leftBool.Value {
				return runtime.Bool{Value: false}, nil
			}
			if expr.Operator == "||" && leftBool.Value {
				return runtime.Bool{Value: true}, nil
			}
			right, err := e.evalExpression(expr.Right, env)
			if err != nil {
				return nil, err
			}
			rightBool, ok := right.(runtime.Bool)
			if !ok {
				return nil, fmt.Errorf("%s expects bool operands", expr.Operator)
			}
			return runtime.Bool{Value: rightBool.Value}, nil
		}
		if expr.Operator == "??" {
			left, err := e.evalExpression(expr.Left, env)
			if err != nil {
				return nil, err
			}
			if _, isNull := left.(runtime.Null); !isNull {
				return left, nil
			}
			return e.evalExpression(expr.Right, env)
		}
		left, err := e.evalExpression(expr.Left, env)
		if err != nil {
			return nil, err
		}
		right, err := e.evalExpression(expr.Right, env)
		if err != nil {
			return nil, err
		}
		return e.evalInfix(expr.Operator, left, right)
	case *ast.AssignmentExpression:
		value, err := e.evalExpression(expr.Value, env)
		if err != nil {
			return nil, err
		}
		switch left := expr.Left.(type) {
		case *ast.Identifier:
			if err := env.Assign(left.Value, value); err != nil {
				return nil, err
			}
		case *ast.IndexExpression:
			if err := e.assignIndex(left, value, env); err != nil {
				return nil, err
			}
		case *ast.SelectorExpression:
			if err := e.assignSelector(left, value, env); err != nil {
				return nil, err
			}
		case *ast.ListLiteral:
			list, ok := value.(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("cannot list-destructure %s", value.TypeName())
			}
			if len(list.Elements) < len(left.Elements) {
				return nil, fmt.Errorf("list has %d elements, destructuring expects %d", len(list.Elements), len(left.Elements))
			}
			for i, element := range left.Elements {
				ident, ok := element.(*ast.Identifier)
				if !ok {
					return nil, fmt.Errorf("list destructuring target must be identifier")
				}
				if err := env.Assign(ident.Value, list.Elements[i]); err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("unsupported assignment target %T", expr.Left)
		}
		return value, nil
	case *ast.CallExpression:
		return e.evalCallWithExpectedType(expr, env, expected)
	case *ast.IndexExpression:
		return e.evalIndexExpression(expr, env)
	case *ast.SelectorExpression:
		return e.evalSelectorExpression(expr, env)
	case *ast.CastExpression:
		value, err := e.evalExpression(expr.Value, env)
		if err != nil {
			return nil, err
		}
		target := e.resolveTypeRef(expr.Type)
		// Nullable cast: `null as ?T` is null; `value as ?T` for
		// non-null falls through to the underlying type's cast logic.
		// Must check before the class-chain match below, otherwise
		// null/T mismatches reject what the user explicitly opted into
		// by writing the `?` prefix.
		if target.Nullable {
			if _, isNull := value.(runtime.Null); isNull {
				return runtime.Null{}, nil
			}
		}
		// Class / interface / parent-chain widening: a value whose
		// class chain contains the target (with the module prefix
		// stripped) is already an instance of the target, so cast
		// is a no-op. Falls through to castValue for primitive
		// conversions when the chain doesn't match.
		if e.valueMatchesType(value, target.Name) {
			return value, nil
		}
		if instance, ok := value.(*runtime.Instance); ok {
			if dunder := castDunderName(target.Name); dunder != "" {
				if result, handled, err := e.invokeInstanceMethod(instance, dunder, nil); err != nil {
					return nil, err
				} else if handled {
					if err := checkCastDunderReturn(target.Name, result); err != nil {
						return nil, thrownError{value: e.withTrace(runtime.Error{Class: "RuntimeError", Message: err.Error()})}
					}
					return result, nil
				}
			}
		}
		return castValue(value, target.Name)
	case *ast.FunctionLiteral:
		// Capture the enclosing scope's type bindings so an inner `T x`
		// parameter or `instanceof T` check can resolve against the
		// outer generic frame's concrete bindings. snapshotTypeBindings
		// walks the env chain so all reachable bindings come along.
		captured := snapshotEnvTypeBindings(env)
		return runtime.Function{Parameters: expr.Parameters, ReturnType: e.resolveTypeRef(expr.ReturnType), Body: expr.Body, Env: env, Async: expr.Async, IsGenerator: blockContainsYield(expr.Body), TypeBindings: captured}, nil
	case *ast.AwaitExpression:
		value, err := e.evalExpression(expr.Value, env)
		if err != nil {
			return nil, err
		}
		return awaitValue(value)
	case *ast.MatchExpression:
		return e.evalMatchExpression(expr, env)
	case *ast.PipeExpression:
		call, ok := ast.LowerPipe(expr)
		if !ok {
			return nil, fmt.Errorf("`|>` right side must be a call, identifier, or selector")
		}
		return e.evalExpression(call, env)
	case *ast.ListComprehension:
		acc := []runtime.Value{}
		if err := e.walkComprehensionClauses(expr.Clauses, 0, env, func(itEnv *runtime.Environment) error {
			v, err := e.evalExpression(expr.Body, itEnv)
			if err != nil {
				return err
			}
			acc = append(acc, v)
			return nil
		}); err != nil {
			return nil, err
		}
		return &runtime.List{Elements: acc}, nil
	case *ast.SetComprehension:
		elements := map[string]runtime.SetEntry{}
		if err := e.walkComprehensionClauses(expr.Clauses, 0, env, func(itEnv *runtime.Environment) error {
			v, err := e.evalExpression(expr.Body, itEnv)
			if err != nil {
				return err
			}
			elements[dictKey(v)] = runtime.SetEntry{Value: v}
			return nil
		}); err != nil {
			return nil, err
		}
		return runtime.Set{Elements: elements}, nil
	case *ast.DictComprehension:
		out := runtime.NewDict()
		if err := e.walkComprehensionClauses(expr.Clauses, 0, env, func(itEnv *runtime.Environment) error {
			key, err := e.evalExpression(expr.KeyBody, itEnv)
			if err != nil {
				return err
			}
			val, err := e.evalExpression(expr.ValueBody, itEnv)
			if err != nil {
				return err
			}
			out.PutEntry(dictKey(key), runtime.DictEntry{Key: key, Value: val})
			return nil
		}); err != nil {
			return nil, err
		}
		return out, nil
	case *ast.TernaryExpression:
		b, err := e.evalBoolCondition(expr.Condition, env)
		if err != nil {
			return nil, err
		}
		if b {
			return e.evalExpression(expr.ThenExpr, env)
		}
		return e.evalExpression(expr.ElseExpr, env)
	default:
		return nil, fmt.Errorf("unsupported expression %T", expr)
	}
}

func (e *Evaluator) evalSelectStatement(stmt *ast.SelectStatement, env *runtime.Environment) (signal, error) {
	handles := make([]*native.ChannelHandle, 0, len(stmt.Cases))
	kinds := make([]string, 0, len(stmt.Cases))
	sendValues := make([]runtime.Value, 0, len(stmt.Cases))
	for _, c := range stmt.Cases {
		chanValue, err := e.evalExpression(c.Channel, env)
		if err != nil {
			return signal{}, err
		}
		handle, err := selectChannelHandle(chanValue)
		if err != nil {
			return signal{}, err
		}
		handles = append(handles, handle)
		kinds = append(kinds, c.Kind)
		if c.Kind == "send" {
			sendValue, err := e.evalExpression(c.Value, env)
			if err != nil {
				return signal{}, err
			}
			sendValues = append(sendValues, sendValue)
		} else {
			sendValues = append(sendValues, nil)
		}
	}
	chosen, recvValue, err := native.SelectChannels(handles, kinds, sendValues, stmt.Default != nil)
	if err != nil {
		return signal{}, fmt.Errorf("select: %s", err.Error())
	}
	if chosen == -1 {
		return e.evalBlock(stmt.Default, env)
	}
	caseEnv := runtime.NewEnclosedEnvironment(env)
	c := stmt.Cases[chosen]
	if c.Kind == "recv" && c.Binding != "" {
		if err := caseEnv.Define(c.Binding, recvValue, true); err != nil {
			return signal{}, err
		}
	}
	return e.evalBlock(c.Body, caseEnv)
}

func selectChannelHandle(v runtime.Value) (*native.ChannelHandle, error) {
	instance, ok := v.(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("select case channel must be a Channel instance")
	}
	handleValue, ok := instance.Fields["_h"]
	if !ok {
		return nil, fmt.Errorf("select case channel is not a Channel (no _h field)")
	}
	h, ok := native.ChannelHandleFromValue(handleValue)
	if !ok {
		return nil, fmt.Errorf("select case channel handle is invalid")
	}
	return h, nil
}

func (e *Evaluator) evalMatchStatement(stmt *ast.MatchStatement, env *runtime.Environment) (signal, error) {
	value, err := e.evalExpression(stmt.Expr, env)
	if err != nil {
		return signal{}, err
	}
	for _, c := range stmt.Cases {
		caseEnv, ok, err := e.matchCase(value, c, env)
		if err != nil {
			return signal{}, err
		}
		if !ok {
			continue
		}
		if c.Body == nil {
			return signal{}, nil
		}
		return e.evalBlock(c.Body, caseEnv)
	}
	hint := matchExhaustivenessHint(stmt.Cases)
	msg := fmt.Sprintf("%s; got %s (type: %s)", hint, value.Inspect(), value.TypeName())
	errValue := e.withTrace(runtime.Error{Class: "MatchError", Message: msg})
	return signal{kind: "throw", thrown: &errValue}, nil
}

func (e *Evaluator) evalMatchExpression(expr *ast.MatchExpression, env *runtime.Environment) (runtime.Value, error) {
	value, err := e.evalExpression(expr.Expr, env)
	if err != nil {
		return nil, err
	}
	for _, c := range expr.Cases {
		caseEnv, ok, err := e.matchCase(value, c, env)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if c.Value == nil {
			return runtime.Null{}, nil
		}
		return e.evalExpression(c.Value, caseEnv)
	}
	hint := matchExhaustivenessHint(expr.Cases)
	msg := fmt.Sprintf("%s; got %s (type: %s)", hint, value.Inspect(), value.TypeName())
	return nil, thrownError{value: runtime.Error{Class: "MatchError", Message: msg}}
}

func matchExhaustivenessHint(cases []ast.MatchCase) string {
	for _, mc := range cases {
		if mc.Default && mc.Guard == nil {
			return "no match case matched"
		}
	}
	return "no match case matched (add a 'default:' case to handle all values)"
}

func (e *Evaluator) matchCase(value runtime.Value, c ast.MatchCase, env *runtime.Environment) (*runtime.Environment, bool, error) {
	caseEnv := runtime.NewEnclosedEnvironment(env)
	matched := false
	switch {
	case c.Default:
		matched = true
	case c.EnumVariant != nil:
		ev := c.EnumVariant
		enumVal, ok := value.(runtime.EnumVariant)
		if ok && strings.EqualFold(enumVal.Enum.Name, ev.Enum.Value) && strings.EqualFold(enumVal.Variant, ev.Variant.Value) {
			matched = true
			for i, param := range ev.Params {
				if i >= len(enumVal.Fields) {
					break
				}
				if err := caseEnv.Define(param.Name.Value, enumVal.Fields[i], false); err != nil {
					return nil, false, err
				}
			}
		}
	case c.Type != nil:
		matched = valueMatchesType(value, c.Type.String())
		if matched && c.Name != nil {
			if err := caseEnv.Define(c.Name.Value, value, false); err != nil {
				return nil, false, err
			}
		}
	case c.ListPattern != nil:
		list, ok := value.(*runtime.List)
		if !ok {
			matched = false
			break
		}
		if len(list.Elements) != len(c.ListPattern.Bindings) {
			matched = false
			break
		}
		matched = true
		for i, binding := range c.ListPattern.Bindings {
			elem := list.Elements[i]
			if binding.Type != nil && !e.valueMatchesType(elem, binding.Type.Name) {
				matched = false
				break
			}
			if binding.Name != nil && binding.Name.Value != "_" {
				if err := caseEnv.Define(binding.Name.Value, elem, false); err != nil {
					return nil, false, err
				}
			}
		}
	case c.Pattern != nil:
		pattern, err := e.evalExpression(c.Pattern, env)
		if err != nil {
			return nil, false, err
		}
		equal, err := e.evalInfix("==", value, pattern)
		if err != nil {
			return nil, false, err
		}
		matched = equal.(runtime.Bool).Value
	}
	if !matched && len(c.Alternates) > 0 {
		for _, alt := range c.Alternates {
			altMatched, err := e.matchOrAlternate(value, alt, env)
			if err != nil {
				return nil, false, err
			}
			if altMatched {
				matched = true
				break
			}
		}
	}
	if !matched {
		return nil, false, nil
	}
	if c.Guard != nil {
		guard, err := e.evalBoolCondition(c.Guard, caseEnv)
		if err != nil {
			return nil, false, err
		}
		if !guard {
			return nil, false, nil
		}
	}
	return caseEnv, true, nil
}

// matchOrAlternate tests a single or-pattern alternate against a value.
// Bare type names (e.g. `int`, stored as an Identifier) test the value's
// type; everything else is evaluated and compared via `==`. Alternates
// never bind, so no env mutation happens here.
func (e *Evaluator) matchOrAlternate(value runtime.Value, alt ast.Expression, env *runtime.Environment) (bool, error) {
	if ident, ok := alt.(*ast.Identifier); ok && isBuiltinTypeNameForMatch(ident.Value) {
		return e.valueMatchesType(value, ident.Value), nil
	}
	patternVal, err := e.evalExpression(alt, env)
	if err != nil {
		return false, err
	}
	eq, err := e.evalInfix("==", value, patternVal)
	if err != nil {
		return false, err
	}
	if b, ok := eq.(runtime.Bool); ok {
		return b.Value, nil
	}
	return false, nil
}

func isBuiltinTypeNameForMatch(name string) bool {
	switch name {
	case "string", "int", "float", "decimal", "bool", "bytes", "list", "dict", "set", "range", "null":
		return true
	}
	return false
}

// valueMatchesType (method form) is the evaluator-aware variant of the
// free function. It walks `errorClassParents` so cross-module
// user-defined error class hierarchies (BadRequestError -> HttpException
// -> RuntimeError, etc.) resolve correctly under `instanceof`. The free
// function is preserved for call sites that don't have an *Evaluator
// in hand and only care about plain class / interface matching.
func (e *Evaluator) valueMatchesType(value runtime.Value, typeName string) bool {
	if dotIdx := strings.Index(typeName, "."); dotIdx >= 0 {
		if ev, ok := value.(runtime.EnumVariant); ok {
			enumTypeName := typeName[:dotIdx]
			variantName := typeName[dotIdx+1:]
			return strings.EqualFold(ev.Enum.Name, enumTypeName) && strings.EqualFold(ev.Variant, variantName)
		}
	}
	stripped := simpleTypeName(typeName)
	if errValue, ok := value.(runtime.Error); ok {
		return e.errorTypeMatches(errValue.Class, stripped)
	}
	return valueMatchesType(value, typeName)
}

func valueMatchesType(value runtime.Value, typeName string) bool {
	if typeName == "any" || typeName == "?any" {
		return true
	}
	if arms, ok := splitTopLevelUnion(typeName); ok {
		for _, arm := range arms {
			if valueMatchesType(value, arm) {
				return true
			}
		}
		return false
	}
	if dotIdx := strings.Index(typeName, "."); dotIdx >= 0 {
		if ev, ok := value.(runtime.EnumVariant); ok {
			enumTypeName := typeName[:dotIdx]
			variantName := typeName[dotIdx+1:]
			return strings.EqualFold(ev.Enum.Name, enumTypeName) && strings.EqualFold(ev.Variant, variantName)
		}
	}
	if baseName, args, ok := splitGenericTypeName(typeName); ok {
		return collectionMatchesGenericType(value, baseName, args)
	}
	typeName = simpleTypeName(typeName)
	if ev, ok := value.(runtime.EnumVariant); ok {
		return strings.EqualFold(ev.Enum.Name, typeName)
	}
	if errValue, ok := value.(runtime.Error); ok {
		return errorTypeMatches(errValue.Class, typeName)
	}
	if instance, ok := value.(*runtime.Instance); ok {
		for class := instance.Class; class != nil; class = class.Parent {
			if typeNamesEqual(class.Name, typeName) {
				return true
			}
			if classImplementsInterface(class, typeName) {
				return true
			}
		}
		for _, extra := range instance.ExtraTypeNames {
			if typeNamesEqual(simpleTypeName(extra), typeName) {
				return true
			}
		}
		// Fall through: an instance with an `__invoke` method matches
		// the `callable` family even when its class isn't named callable.
		if isCallableTypeName(typeName) && runtime.IsCallableValue(value) {
			return true
		}
		return false
	}
	// `func` / `callable` / `function` all match any callable runtime value
	// (Function, OverloadedFunction, BytecodeFunction, decorated targets).
	// This keeps `as callable` symmetrical with parameter-type matching
	// and with the VM's cast path, both of which already accept funcs.
	if isCallableTypeName(typeName) && runtime.IsCallableValue(value) {
		return true
	}
	return typeNamesEqual(value.TypeName(), typeName)
}

// splitGenericTypeName splits "list<int>" / "dict<string,int>" / "?list<int>"
// into ("list", ["int"], true) / ("dict", ["string","int"], true) /
// ("list", ["int"], true). Returns (_, _, false) when the input has no
// generic-arg clause.
func splitGenericTypeName(typeName string) (string, []string, bool) {
	if strings.HasPrefix(typeName, "?") {
		typeName = typeName[1:]
	}
	lt := strings.IndexByte(typeName, '<')
	if lt < 0 || !strings.HasSuffix(typeName, ">") {
		return "", nil, false
	}
	base := typeName[:lt]
	inner := typeName[lt+1 : len(typeName)-1]
	// Split top-level commas, ignoring those inside nested generics.
	var args []string
	depth := 0
	start := 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(inner[start:i]))
				start = i + 1
			}
		}
	}
	if start <= len(inner) {
		args = append(args, strings.TrimSpace(inner[start:]))
	}
	return base, args, true
}

func collectionMatchesGenericType(value runtime.Value, base string, args []string) bool {
	switch v := value.(type) {
	case *runtime.List:
		if base != "list" || len(args) != 1 {
			return false
		}
		if len(v.ElementTypes) >= 1 {
			return typeNameSatisfies(v.ElementTypes[0], args[0])
		}
		for _, el := range v.Elements {
			if !valueMatchesType(el, args[0]) {
				return false
			}
		}
		return true
	case runtime.Set:
		if base != "set" || len(args) != 1 {
			return false
		}
		if len(v.ElementTypes) >= 1 {
			return typeNameSatisfies(v.ElementTypes[0], args[0])
		}
		for _, e := range v.Elements {
			if !valueMatchesType(e.Value, args[0]) {
				return false
			}
		}
		return true
	case runtime.Dict:
		if base != "dict" || len(args) != 2 {
			return false
		}
		if len(v.ElementTypes) >= 2 {
			return typeNameSatisfies(v.ElementTypes[0], args[0]) && typeNameSatisfies(v.ElementTypes[1], args[1])
		}
		for _, e := range v.Entries {
			if !valueMatchesType(e.Key, args[0]) {
				return false
			}
			if !valueMatchesType(e.Value, args[1]) {
				return false
			}
		}
		return true
	}
	return false
}

// splitTopLevelUnion splits a union on depth-0 `|`, preserving `|`
// inside nested generic angle brackets.
func splitTopLevelUnion(typeName string) ([]string, bool) {
	depth := 0
	hasTopLevel := false
	for i := 0; i < len(typeName); i++ {
		switch typeName[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 {
				hasTopLevel = true
			}
		}
	}
	if !hasTopLevel {
		return nil, false
	}
	var arms []string
	depth = 0
	start := 0
	for i := 0; i < len(typeName); i++ {
		switch typeName[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 {
				arms = append(arms, strings.TrimSpace(typeName[start:i]))
				start = i + 1
			}
		}
	}
	arms = append(arms, strings.TrimSpace(typeName[start:]))
	return arms, true
}

// typeNameSatisfies handles `any` and union arms over the existing
// invariance-by-name rule for tagged collections.
func typeNameSatisfies(have, want string) bool {
	if want == "any" || want == "?any" {
		return true
	}
	if arms, ok := splitTopLevelUnion(want); ok {
		for _, arm := range arms {
			if typeNameSatisfies(have, arm) {
				return true
			}
		}
		return false
	}
	return typeNamesEqual(have, want)
}

func mergeInterfaceMembers(stmt *ast.ClassStatement, ifaces []*runtime.Interface) error {
	declaredMethods := map[string]bool{}
	declaredFields := map[string]bool{}
	for _, member := range stmt.Members {
		switch m := member.(type) {
		case *ast.FunctionStatement:
			declaredMethods[strings.ToLower(m.Name.Value)] = true
		case *ast.DeclarationStatement:
			if !strings.HasPrefix(m.Kind, "static") {
				declaredFields[strings.ToLower(m.Name.Value)] = true
			}
		}
	}
	defaultSource := map[string]string{}
	defaultMethod := map[string]*ast.FunctionStatement{}
	fieldSource := map[string]string{}
	fieldDecl := map[string]*ast.DeclarationStatement{}
	for _, iface := range ifaces {
		for _, def := range iface.Defaults {
			key := strings.ToLower(def.Name.Value)
			if declaredMethods[key] {
				continue
			}
			if prev, seen := defaultSource[key]; seen && prev != iface.Name {
				return fmt.Errorf("class %s inherits multiple defaults for %s from %s and %s; class must override", stmt.Name.Value, def.Name.Value, prev, iface.Name)
			}
			defaultSource[key] = iface.Name
			defaultMethod[key] = def
		}
		for _, field := range iface.Fields {
			key := strings.ToLower(field.Name.Value)
			if declaredFields[key] {
				continue
			}
			if prev, seen := fieldSource[key]; seen {
				prevField := fieldDecl[key]
				if prevField.Type.String() != field.Type.String() {
					return fmt.Errorf("class %s inherits field %s from %s (%s) and %s (%s) with conflicting types", stmt.Name.Value, field.Name.Value, prev, prevField.Type.String(), iface.Name, field.Type.String())
				}
				continue
			}
			fieldSource[key] = iface.Name
			fieldDecl[key] = field
		}
	}
	for _, field := range fieldDecl {
		stmt.Members = append(stmt.Members, field)
	}
	for _, method := range defaultMethod {
		stmt.Members = append(stmt.Members, method)
	}
	return nil
}

func (e *Evaluator) buildInterface(stmt *ast.InterfaceStatement, env *runtime.Environment) (*runtime.Interface, error) {
	iface := &runtime.Interface{Name: stmt.Name.Value, Doc: stmt.Doc, TypeParameters: typeParameterNames(stmt.Generics), Methods: e.resolveFunctionSignatures(stmt.Methods), Defaults: stmt.Defaults, Fields: stmt.Fields}
	for _, parentRef := range stmt.Parents {
		parentValue, ok, err := e.resolveTypeValue(parentRef, env)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("parent interface %q is not declared", parentRef.Name)
		}
		parent, ok := parentValue.(*runtime.Interface)
		if !ok {
			return nil, fmt.Errorf("%q is not an interface", parentRef.Name)
		}
		iface.Parents = append(iface.Parents, parent)
	}
	return iface, nil
}

func (e *Evaluator) getErrorSentinel(name string) *runtime.Class {
	if cls, ok := e.errorSentinels[name]; ok {
		return cls
	}
	var parent *runtime.Class
	if pname := errorParent(name); pname != "" {
		parent = e.getErrorSentinel(pname)
	}
	cls := &runtime.Class{
		Name:          name,
		Fields:        []runtime.Field{},
		Methods:       map[string][]runtime.Function{},
		StaticMethods: map[string][]runtime.Function{},
		StaticValues:  map[string]runtime.Value{},
		Parent:        parent,
	}
	e.errorSentinels[name] = cls
	return cls
}

func (e *Evaluator) installBuiltinTypes(env *runtime.Environment) error {
	testClass := &runtime.Class{
		Name:    "Test",
		Fields:  []runtime.Field{},
		Methods: map[string][]runtime.Function{},
		Env:     env,
	}
	for _, methodName := range []string{
		"equal",
		"assertEqual",
		"assertEquals",
		"assertNotEqual",
		"assertNotEquals",
		"isTrue",
		"assertTrue",
		"isFalse",
		"assertFalse",
		"assertNull",
		"notNull",
		"assertNotNull",
		"assertContains",
		"assertNotContains",
		"assertEmpty",
		"assertNotEmpty",
		"assertGreaterThan",
		"assertGreaterThanOrEqual",
		"assertLessThan",
		"assertLessThanOrEqual",
		"assertThrows",
		"assertThrowsOf",
		"fail",
	} {
		testClass.Methods[strings.ToLower(methodName)] = []runtime.Function{{Name: methodName, Native: e.nativeTestAssertion(methodName)}}
	}
	e.testClass = testClass
	classes := httpObjectClasses(env)
	for _, class := range classes {
		if strings.EqualFold(class.Name, "Request") {
			class.Module = "http"
			e.httpRequestClass = class
		}
		if strings.EqualFold(class.Name, "Response") {
			class.Module = "http"
			e.httpResponseClass = class
		}
	}
	for _, class := range e.processObjectClasses() {
		if strings.EqualFold(class.Name, "Process") {
			e.processClass = class
		}
		if strings.EqualFold(class.Name, "Result") {
			e.processResultClass = class
		}
	}
	for _, class := range e.httpClientObjectClasses() {
		class.Module = "http"
		switch strings.ToLower(class.Name) {
		case "client":
			e.httpClientClass = class
		case "builder":
			e.httpBuilderClass = class
		case "cookiejar":
			e.httpCookieJarClass = class
		case "fetchstream":
			e.httpFetchStreamClass = class
		}
	}
	for _, class := range e.dbObjectClasses(env) {
		switch strings.ToLower(class.Name) {
		case "connection":
			e.dbConnectionClass = class
		case "transaction":
			e.dbTransactionClass = class
		case "statement":
			e.dbStatementClass = class
		case "rows":
			e.dbRowsClass = class
		}
	}
	e.streamIfaces = map[string]*runtime.Interface{}
	for _, iface := range streamInterfaces() {
		e.streamIfaces[strings.ToLower(iface.Name)] = iface
	}
	return nil
}

func httpHeadersObject(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects optional dict or http.Headers", call.Callee.String())
	}
	if len(args) == 0 {
		return runtime.HTTPHeaders{Values: map[string][]string{}}, nil
	}
	headers, err := httpHeadersFromValue(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %v", call.Callee.String(), err)
	}
	return headers, nil
}

func httpCookieObject(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects dict, Set-Cookie string, or http.Cookie", call.Callee.String())
	}
	cookie, err := native.HTTPCookieFromValue(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %v", call.Callee.String(), err)
	}
	return cookie, nil
}

func httpHeadersFromValue(value runtime.Value) (runtime.HTTPHeaders, error) {
	switch value := value.(type) {
	case runtime.HTTPHeaders:
		return copyHTTPHeaders(value), nil
	case runtime.Dict:
		out := runtime.HTTPHeaders{Values: map[string][]string{}}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return out, fmt.Errorf("headers keys must be strings")
			}
			switch headerValue := entry.Value.(type) {
			case runtime.String:
				out.Values[http.CanonicalHeaderKey(key.Value)] = []string{headerValue.Value}
			case *runtime.List:
				values := make([]string, 0, len(headerValue.Elements))
				for _, element := range headerValue.Elements {
					text, ok := element.(runtime.String)
					if !ok {
						return out, fmt.Errorf("headers list values must be strings")
					}
					values = append(values, text.Value)
				}
				out.Values[http.CanonicalHeaderKey(key.Value)] = values
			default:
				return out, fmt.Errorf("headers values must be strings or list<string>")
			}
		}
		return out, nil
	default:
		return runtime.HTTPHeaders{}, fmt.Errorf("headers must be dict or http.Headers")
	}
}

func copyHTTPHeaders(headers runtime.HTTPHeaders) runtime.HTTPHeaders {
	out := runtime.HTTPHeaders{Values: map[string][]string{}}
	for key, values := range headers.Values {
		out.Values[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	return out
}

func httpHeadersToDict(headers runtime.HTTPHeaders) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	for key, values := range headers.Values {
		keyValue := runtime.String{Value: http.CanonicalHeaderKey(key)}
		var value runtime.Value
		if len(values) == 1 {
			value = runtime.String{Value: values[0]}
		} else {
			elements := make([]runtime.Value, len(values))
			for i, item := range values {
				elements[i] = runtime.String{Value: item}
			}
			value = &runtime.List{Elements: elements}
		}
		entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
	}
	return runtime.Dict{Entries: entries}
}

func httpHeaderValue(value runtime.Value) (runtime.HTTPHeaders, bool) {
	headers, ok := value.(runtime.HTTPHeaders)
	if ok {
		return headers, true
	}
	dict, ok := value.(runtime.Dict)
	if !ok {
		return runtime.HTTPHeaders{}, false
	}
	headers, err := httpHeadersFromValue(dict)
	return headers, err == nil
}

func httpHeadersMethod(receiver runtime.HTTPHeaders, name string, args []runtime.Value) (runtime.Value, error) {
	headers := copyHTTPHeaders(receiver)
	switch name {
	case "get":
		key, err := singleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		values := headers.Values[key]
		if len(values) == 0 {
			return runtime.Null{}, nil
		}
		return runtime.String{Value: values[0]}, nil
	case "getAll":
		key, err := singleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		values := headers.Values[key]
		elements := make([]runtime.Value, len(values))
		for i, value := range values {
			elements[i] = runtime.String{Value: value}
		}
		return &runtime.List{Elements: elements}, nil
	case "has":
		key, err := singleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: len(headers.Values[key]) > 0}, nil
	case "set":
		if len(args) != 2 {
			return nil, fmt.Errorf("http.Headers.set expects name and value")
		}
		key, value, err := headerNameValue("set", args)
		if err != nil {
			return nil, err
		}
		headers.Values[key] = []string{value}
		return headers, nil
	case "add":
		if len(args) != 2 {
			return nil, fmt.Errorf("http.Headers.add expects name and value")
		}
		key, value, err := headerNameValue("add", args)
		if err != nil {
			return nil, err
		}
		headers.Values[key] = append(headers.Values[key], value)
		return headers, nil
	case "delete":
		key, err := singleHeaderName(name, args)
		if err != nil {
			return nil, err
		}
		delete(headers.Values, key)
		return headers, nil
	case "keys":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Headers.keys expects no arguments")
		}
		keys := make([]string, 0, len(headers.Values))
		for key := range headers.Values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		elements := make([]runtime.Value, len(keys))
		for i, key := range keys {
			elements[i] = runtime.String{Value: key}
		}
		return &runtime.List{Elements: elements}, nil
	case "toDict":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Headers.toDict expects no arguments")
		}
		return httpHeadersToDict(headers), nil
	default:
		return nil, fmt.Errorf("http.Headers has no method %s", name)
	}
}

func singleHeaderName(method string, args []runtime.Value) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("http.Headers.%s expects name", method)
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("http.Headers.%s name must be string", method)
	}
	return http.CanonicalHeaderKey(name.Value), nil
}

func headerNameValue(method string, args []runtime.Value) (string, string, error) {
	name, ok := args[0].(runtime.String)
	if !ok {
		return "", "", fmt.Errorf("http.Headers.%s name must be string", method)
	}
	value, ok := args[1].(runtime.String)
	if !ok {
		return "", "", fmt.Errorf("http.Headers.%s value must be string", method)
	}
	return http.CanonicalHeaderKey(name.Value), value.Value, nil
}

func httpObjectClasses(env *runtime.Environment) []*runtime.Class {
	requestClass := &runtime.Class{
		Name: "Request",
		Fields: []runtime.Field{
			{Name: "method"},
			{Name: "path"},
			{Name: "query"},
			{Name: "remoteAddr"},
			{Name: "body"},
			{Name: "headers"},
		},
		Methods: map[string][]runtime.Function{
			"header":    []runtime.Function{{Name: "header", Native: nativeRequestHeader}},
			"json":      []runtime.Function{{Name: "json", Native: nativeRequestJSON}},
			"bodytext":  []runtime.Function{{Name: "bodyText", Native: nativeRequestBodyText}},
			"bodybytes": []runtime.Function{{Name: "bodyBytes", Native: nativeRequestBodyBytes}},
			"todict":    []runtime.Function{{Name: "toDict", Native: nativeRequestToDict}},
			"inspect":   []runtime.Function{{Name: "inspect", Native: nativeRequestInspect}},
		},
		Constructors: []runtime.Function{{Name: "Request", Native: nativeRequestConstructor}},
		Env:          env,
	}
	responseClass := &runtime.Class{
		Name: "Response",
		Fields: []runtime.Field{
			{Name: "status"},
			{Name: "body"},
			{Name: "headers"},
		},
		Methods: map[string][]runtime.Function{
			"withheader": []runtime.Function{{Name: "withHeader", Native: nativeResponseWithHeader}},
			"withbody":   []runtime.Function{{Name: "withBody", Native: nativeResponseWithBody}},
			"withstatus": []runtime.Function{{Name: "withStatus", Native: nativeResponseWithStatus}},
			"todict":     []runtime.Function{{Name: "toDict", Native: nativeResponseToDict}},
			"inspect":    []runtime.Function{{Name: "inspect", Native: nativeResponseInspect}},
		},
		Constructors: []runtime.Function{{Name: "Response", Native: nativeResponseConstructor}},
		Env:          env,
	}
	return []*runtime.Class{requestClass, responseClass}
}

func nativeRequestConstructor(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("Request constructor expects optional dict")
	}
	if len(args) == 0 {
		return runtime.Null{}, nil
	}
	dict, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("Request constructor expects dict")
	}
	for key, value := range fieldsFromEntries(dict.Entries) {
		this.Fields[key] = value
	}
	return runtime.Null{}, nil
}

func nativeRequestHeader(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("Request.header expects one argument")
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("Request.header name must be string")
	}
	headers, ok := httpHeaderValue(this.Fields["headers"])
	if !ok {
		return runtime.Null{}, nil
	}
	values := headers.Values[http.CanonicalHeaderKey(name.Value)]
	if len(values) == 0 {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: values[0]}, nil
}

func nativeRequestJSON(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("Request.json expects no arguments")
	}
	body, err := requestBodyText(this)
	if err != nil {
		return nil, err
	}
	value, parseErr := native.ParseJSONText(body)
	if parseErr != nil {
		return nil, fmt.Errorf("%s", parseErr.Message)
	}
	return value, nil
}

func nativeRequestBodyText(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("Request.bodyText expects no arguments")
	}
	body, err := requestBodyText(this)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: body}, nil
}

func nativeRequestBodyBytes(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("Request.bodyBytes expects no arguments")
	}
	switch body := this.Fields["body"].(type) {
	case runtime.String:
		return runtime.Bytes{Value: []byte(body.Value)}, nil
	case runtime.Bytes:
		return body, nil
	case runtime.Null:
		return runtime.Bytes{}, nil
	default:
		return nil, fmt.Errorf("Request.body must be string or bytes")
	}
}

func requestBodyText(this *runtime.Instance) (string, error) {
	switch body := this.Fields["body"].(type) {
	case runtime.String:
		return body.Value, nil
	case runtime.Bytes:
		return string(body.Value), nil
	case runtime.Null:
		return "", nil
	default:
		return "", fmt.Errorf("Request.body must be string or bytes")
	}
}

func nativeRequestToDict(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("Request.toDict expects no arguments")
	}
	entries := map[string]runtime.DictEntry{}
	for _, name := range []string{"method", "path", "query", "remoteAddr", "body", "headers"} {
		value, ok := this.Fields[name]
		if !ok {
			value = runtime.Null{}
		}
		if name == "headers" {
			if headers, ok := httpHeaderValue(value); ok {
				value = httpHeadersToDict(headers)
			}
		}
		putDict(entries, name, value)
	}
	return runtime.Dict{Entries: entries}, nil
}

func nativeRequestInspect(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("Request.inspect expects no arguments")
	}
	method, _ := this.Fields["method"].(runtime.String)
	path, _ := this.Fields["path"].(runtime.String)
	return runtime.String{Value: method.Value + " " + path.Value}, nil
}

func nativeResponseConstructor(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	value, err := buildResponseInstance(this.Class, "Response constructor", args)
	if err != nil {
		return nil, err
	}
	instance := value.(*runtime.Instance)
	this.Fields = instance.Fields
	return runtime.Null{}, nil
}

func nativeResponseWithHeader(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Response.withHeader expects name and value")
	}
	name, nameOK := args[0].(runtime.String)
	value, valueOK := args[1].(runtime.String)
	if !nameOK || !valueOK {
		return nil, fmt.Errorf("Response.withHeader expects string name and value")
	}
	headers := map[string]runtime.DictEntry{}
	if existing, ok := httpHeaderValue(this.Fields["headers"]); ok {
		headers = httpHeadersToDict(existing).Entries
	}
	putDict(headers, name.Value, value)
	return newResponseInstance(this.Class, this.Fields["status"], this.Fields["body"], runtime.Dict{Entries: headers}), nil
}

func nativeResponseWithBody(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("Response.withBody expects one argument")
	}
	return newResponseInstance(this.Class, this.Fields["status"], args[0], this.Fields["headers"]), nil
}

func nativeResponseWithStatus(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("Response.withStatus expects one argument")
	}
	if _, ok := toInt64(args[0]); !ok {
		return nil, fmt.Errorf("Response.withStatus status must be int")
	}
	return newResponseInstance(this.Class, args[0], this.Fields["body"], this.Fields["headers"]), nil
}

func nativeResponseToDict(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("Response.toDict expects no arguments")
	}
	return responseInstanceDict(this), nil
}

func nativeResponseInspect(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("Response.inspect expects no arguments")
	}
	status := "200"
	if value, ok := this.Fields["status"].(runtime.Int); ok {
		status = value.Value.String()
	}
	return runtime.String{Value: "HTTP " + status}, nil
}

func (e *Evaluator) processObjectClasses() []*runtime.Class {
	resultClass := &runtime.Class{
		Name:   "Result",
		Module: "process",
		Fields: []runtime.Field{
			{Name: "code"}, {Name: "stdout"}, {Name: "stderr"}, {Name: "timedOut"},
		},
		Methods: map[string][]runtime.Function{},
	}
	resultClass.Methods["isok"] = []runtime.Function{{Name: "isOk", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		code, ok := this.Fields["code"].(runtime.Int)
		if !ok {
			return runtime.Bool{Value: false}, nil
		}
		return runtime.Bool{Value: code.Value.Sign() == 0}, nil
	}}}
	resultClass.Methods["code"] = []runtime.Function{{Name: "code", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		return this.Fields["code"], nil
	}}}
	resultClass.Methods["stdout"] = []runtime.Function{{Name: "stdout", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		return this.Fields["stdout"], nil
	}}}
	resultClass.Methods["stderr"] = []runtime.Function{{Name: "stderr", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		return this.Fields["stderr"], nil
	}}}
	resultClass.Methods["timedout"] = []runtime.Function{{Name: "timedOut", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		return this.Fields["timedOut"], nil
	}}}

	processClass := &runtime.Class{
		Name:    "Process",
		Module:  "process",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
	}
	getHandle := func(this *runtime.Instance) (*processHandle, error) {
		return e.processHandle(this.Fields["handle"])
	}
	processClass.Methods["write"] = []runtime.Function{{Name: "write", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Process.write expects 1 argument")
		}
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Process.write argument must be string")
		}
		n, err := io.WriteString(proc.stdin, text.Value)
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(int64(n)), nil
	}}}
	processClass.Methods["closestdin"] = []runtime.Function{{Name: "closeStdin", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		return runtime.Null{}, proc.stdin.Close()
	}}}
	processClass.Methods["readstdout"] = []runtime.Function{{Name: "readStdout", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(proc.stdout)
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: string(data)}, nil
	}}}
	processClass.Methods["readstderr"] = []runtime.Function{{Name: "readStderr", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(proc.stderr)
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: string(data)}, nil
	}}}
	processClass.Methods["readstdoutn"] = []runtime.Function{{Name: "readStdoutN", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Process.readStdoutN expects 1 argument")
		}
		n, ok := args[0].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("Process.readStdoutN argument must be int")
		}
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n.Value.Int64())
		nr, _ := proc.stdout.Read(buf)
		return runtime.String{Value: string(buf[:nr])}, nil
	}}}
	processClass.Methods["readstderrn"] = []runtime.Function{{Name: "readStderrN", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Process.readStderrN expects 1 argument")
		}
		n, ok := args[0].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("Process.readStderrN argument must be int")
		}
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n.Value.Int64())
		nr, _ := proc.stderr.Read(buf)
		return runtime.String{Value: string(buf[:nr])}, nil
	}}}
	processClass.Methods["wait"] = []runtime.Function{{Name: "wait", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		handle, err := processHandleID(this.Fields["handle"])
		if err != nil {
			return nil, err
		}
		e.processMu.Lock()
		proc, ok := e.processes[handle]
		if ok {
			delete(e.processes, handle)
		}
		e.processMu.Unlock()
		if !ok {
			return nil, fmt.Errorf("unknown process handle %d", handle)
		}
		waitErr := proc.cmd.Wait()
		if proc.cancel != nil {
			proc.cancel()
		}
		code := int64(0)
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				code = int64(exitErr.ExitCode())
			} else {
				return nil, waitErr
			}
		}
		return runtime.NewInt64(code), nil
	}}}
	processClass.Methods["kill"] = []runtime.Function{{Name: "kill", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		if proc.cancel != nil {
			proc.cancel()
		}
		return runtime.Null{}, proc.cmd.Process.Kill()
	}}}
	processClass.Methods["signal"] = []runtime.Function{{Name: "signal", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Process.signal expects 1 argument")
		}
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		name, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Process.signal name must be string")
		}
		sig, err := signalByName(name.Value)
		if err != nil {
			return nil, err
		}
		if proc.cancel != nil && sig == syscall.SIGKILL {
			proc.cancel()
		}
		return runtime.Null{}, proc.cmd.Process.Signal(sig)
	}}}
	processClass.Methods["pid"] = []runtime.Function{{Name: "pid", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		proc, err := getHandle(this)
		if err != nil {
			return nil, err
		}
		if proc.cmd.Process == nil {
			return nil, fmt.Errorf("process has not started")
		}
		return runtime.NewInt64(int64(proc.cmd.Process.Pid)), nil
	}}}

	return []*runtime.Class{processClass, resultClass}
}

func (e *Evaluator) dbObjectClasses(env *runtime.Environment) []*runtime.Class {
	rowsClass := &runtime.Class{
		Name:    "Rows",
		Module:  "db",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
		Env:     env,
	}
	rowsClass.Methods["next"] = []runtime.Function{{Name: "next", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.next expects no arguments")
		}
		return e.dbRowsNext(this)
	}}}
	rowsClass.Methods["row"] = []runtime.Function{{Name: "row", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.row expects no arguments")
		}
		h, err := e.dbRowsHandle(this)
		if err != nil {
			return nil, err
		}
		if h.current == nil {
			return runtime.Null{}, nil
		}
		return h.current, nil
	}}}
	rowsClass.Methods["columns"] = []runtime.Function{{Name: "columns", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.columns expects no arguments")
		}
		h, err := e.dbRowsHandle(this)
		if err != nil {
			return nil, err
		}
		columns := make([]runtime.Value, 0, len(h.columns))
		for _, column := range h.columns {
			columns = append(columns, runtime.String{Value: column})
		}
		return &runtime.List{Elements: columns}, nil
	}}}
	rowsClass.Methods["close"] = []runtime.Function{{Name: "close", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.close expects no arguments")
		}
		return e.dbRowsClose(this)
	}}}
	rowsClass.Methods["all"] = []runtime.Function{{Name: "all", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.all expects no arguments")
		}
		return e.dbRowsAll(this)
	}}}
	rowsClass.Methods["length"] = []runtime.Function{{Name: "length", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.length expects no arguments")
		}
		rows, err := e.dbRowsAll(this)
		if err != nil {
			return nil, err
		}
		list := rows.(*runtime.List)
		return runtime.SmallInt{Value: int64(len(list.Elements))}, nil
	}}}
	rowsClass.Methods["isempty"] = []runtime.Function{{Name: "isEmpty", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.isEmpty expects no arguments")
		}
		first, err := e.dbRowsFirst(this)
		if err != nil {
			return nil, err
		}
		_, empty := first.(runtime.Null)
		return runtime.Bool{Value: empty}, nil
	}}}
	rowsClass.Methods["get"] = []runtime.Function{{Name: "get", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Rows.get expects index")
		}
		index, err := rawInt64(args[0], "index")
		if err != nil {
			return nil, err
		}
		return e.dbRowsGet(this, index)
	}}}
	rowsClass.Methods["first"] = []runtime.Function{{Name: "first", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.first expects no arguments")
		}
		return e.dbRowsFirst(this)
	}}}
	rowsClass.Methods["tolist"] = []runtime.Function{{Name: "toList", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Rows.toList expects no arguments")
		}
		return e.dbRowsAll(this)
	}}}

	transactionClass := &runtime.Class{
		Name:    "Transaction",
		Module:  "db",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
		Env:     env,
	}
	transactionClass.Methods["exec"] = []runtime.Function{{Name: "exec", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbTxExecStandard(syntheticCall("db.Transaction.exec"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	transactionClass.Methods["query"] = []runtime.Function{{Name: "query", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbTxQueryRows(syntheticCall("db.Transaction.query"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	transactionClass.Methods["commit"] = []runtime.Function{{Name: "commit", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Transaction.commit expects no arguments")
		}
		return e.dbCommit(syntheticCall("db.Transaction.commit"), []runtime.Value{this.Fields["handle"]})
	}}}
	transactionClass.Methods["rollback"] = []runtime.Function{{Name: "rollback", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Transaction.rollback expects no arguments")
		}
		return e.dbRollback(syntheticCall("db.Transaction.rollback"), []runtime.Value{this.Fields["handle"]})
	}}}

	statementClass := &runtime.Class{
		Name:    "Statement",
		Module:  "db",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
		Env:     env,
	}
	statementClass.Methods["exec"] = []runtime.Function{{Name: "exec", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbStmtExecStandard(syntheticCall("db.Statement.exec"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	statementClass.Methods["query"] = []runtime.Function{{Name: "query", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbStmtQueryRows(syntheticCall("db.Statement.query"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	statementClass.Methods["close"] = []runtime.Function{{Name: "close", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Statement.close expects no arguments")
		}
		return e.dbStmtClose(syntheticCall("db.Statement.close"), []runtime.Value{this.Fields["handle"]})
	}}}

	connectionClass := &runtime.Class{
		Name:    "Connection",
		Module:  "db",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
		Constructors: []runtime.Function{{Name: "Connection", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			handle, err := e.dbOpen(syntheticCall("db.Connection"), args)
			if err != nil {
				return nil, err
			}
			this.Fields["handle"] = handle
			return runtime.Null{}, nil
		}}},
		Env: env,
	}
	connectionClass.Methods["exec"] = []runtime.Function{{Name: "exec", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbExecStandard(syntheticCall("db.Connection.exec"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	connectionClass.Methods["query"] = []runtime.Function{{Name: "query", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbQueryRows(syntheticCall("db.Connection.query"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	connectionClass.Methods["begin"] = []runtime.Function{{Name: "begin", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Connection.begin expects no arguments")
		}
		handle, err := e.dbBegin(syntheticCall("db.Connection.begin"), []runtime.Value{this.Fields["handle"]})
		if err != nil {
			return nil, err
		}
		return &runtime.Instance{Class: transactionClass, Fields: map[string]runtime.Value{"handle": handle}}, nil
	}}}
	connectionClass.Methods["prepare"] = []runtime.Function{{Name: "prepare", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		handle, err := e.dbPrepare(syntheticCall("db.Connection.prepare"), append([]runtime.Value{this.Fields["handle"]}, args...))
		if err != nil {
			return nil, err
		}
		return &runtime.Instance{Class: statementClass, Fields: map[string]runtime.Value{"handle": handle}}, nil
	}}}
	connectionClass.Methods["configure"] = []runtime.Function{{Name: "configure", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbConfigure(syntheticCall("db.Connection.configure"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	connectionClass.Methods["stats"] = []runtime.Function{{Name: "stats", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Connection.stats expects no arguments")
		}
		return e.dbStats(syntheticCall("db.Connection.stats"), []runtime.Value{this.Fields["handle"]})
	}}}
	connectionClass.Methods["migrate"] = []runtime.Function{{Name: "migrate", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		return e.dbMigrate(syntheticCall("db.Connection.migrate"), append([]runtime.Value{this.Fields["handle"]}, args...))
	}}}
	connectionClass.Methods["close"] = []runtime.Function{{Name: "close", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("Connection.close expects no arguments")
		}
		return e.dbClose(syntheticCall("db.Connection.close"), []runtime.Value{this.Fields["handle"]})
	}}}

	return []*runtime.Class{connectionClass, transactionClass, statementClass, rowsClass}
}

func syntheticCall(name string) *ast.CallExpression {
	return &ast.CallExpression{Callee: &ast.Identifier{Value: name}}
}

func (e *Evaluator) registerDBRows(rows *sql.Rows) (runtime.Value, error) {
	columns, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	class := e.dbRowsClass
	if class == nil && e.parent != nil {
		class = e.parent.dbRowsClass
	}
	if class == nil {
		_ = rows.Close()
		return nil, fmt.Errorf("Rows class is not initialized")
	}
	e.dbMu.Lock()
	e.nextDBRowsID++
	id := e.nextDBRowsID
	e.dbRows[id] = &dbRowsHandle{rows: rows, columns: columns}
	e.dbMu.Unlock()
	return &runtime.Instance{Class: class, Fields: map[string]runtime.Value{"handle": runtime.NewInt64(id)}}, nil
}

func (e *Evaluator) dbRowsHandle(instance *runtime.Instance) (*dbRowsHandle, error) {
	id, ok := instance.Fields["handle"].(runtime.Int)
	if !ok || !id.Value.IsInt64() {
		return nil, fmt.Errorf("Rows has invalid backing handle")
	}
	handle := id.Value.Int64()
	e.dbMu.Lock()
	rows, ok := e.dbRows[handle]
	e.dbMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.dbRowsHandle(instance)
	}
	if !ok {
		return nil, fmt.Errorf("unknown Rows handle %d", handle)
	}
	return rows, nil
}

func (e *Evaluator) dbRowsClose(instance *runtime.Instance) (runtime.Value, error) {
	rows, err := e.dbRowsHandle(instance)
	if err != nil {
		return nil, err
	}
	if rows.closed {
		return runtime.Null{}, nil
	}
	rows.closed = true
	rows.exhausted = true
	return runtime.Null{}, rows.rows.Close()
}

func (e *Evaluator) dbRowsNext(instance *runtime.Instance) (runtime.Value, error) {
	rows, err := e.dbRowsHandle(instance)
	if err != nil {
		return nil, err
	}
	if rows.closed || rows.exhausted {
		rows.current = nil
		return runtime.Bool{Value: false}, nil
	}
	if !rows.rows.Next() {
		rows.current = nil
		rows.exhausted = true
		rows.closed = true
		if err := rows.rows.Err(); err != nil {
			_ = rows.rows.Close()
			return nil, err
		}
		if err := rows.rows.Close(); err != nil {
			return nil, err
		}
		return runtime.Bool{Value: false}, nil
	}
	row, err := scanSQLRow(rows.rows, rows.columns)
	if err != nil {
		return nil, err
	}
	rows.current = row
	rows.cache = append(rows.cache, row)
	return runtime.Bool{Value: true}, nil
}

func (e *Evaluator) dbRowsAll(instance *runtime.Instance) (runtime.Value, error) {
	rows, err := e.dbRowsHandle(instance)
	if err != nil {
		return nil, err
	}
	for !rows.closed && !rows.exhausted {
		next, err := e.dbRowsNext(instance)
		if err != nil {
			return nil, err
		}
		ok, _ := next.(runtime.Bool)
		if !ok.Value {
			break
		}
	}
	out := append([]runtime.Value(nil), rows.cache...)
	return &runtime.List{Elements: out}, nil
}

func (e *Evaluator) dbRowsFirst(instance *runtime.Instance) (runtime.Value, error) {
	return e.dbRowsGet(instance, 0)
}

func (e *Evaluator) dbRowsGet(instance *runtime.Instance, index int64) (runtime.Value, error) {
	if index < 0 {
		return runtime.Null{}, nil
	}
	rows, err := e.dbRowsHandle(instance)
	if err != nil {
		return nil, err
	}
	for int64(len(rows.cache)) <= index && !rows.closed && !rows.exhausted {
		next, err := e.dbRowsNext(instance)
		if err != nil {
			return nil, err
		}
		ok, _ := next.(runtime.Bool)
		if !ok.Value {
			break
		}
	}
	if index >= int64(len(rows.cache)) {
		return runtime.Null{}, nil
	}
	return rows.cache[int(index)], nil
}

func (e *Evaluator) newProcessResult(raw runtime.Value) (runtime.Value, error) {
	dict, ok := raw.(runtime.Dict)
	if !ok {
		return raw, nil
	}
	inst := &runtime.Instance{Class: e.processResultClass, Fields: map[string]runtime.Value{}}
	if v, ok := dictField(dict, "code"); ok {
		inst.Fields["code"] = v
	}
	if v, ok := dictField(dict, "stdout"); ok {
		inst.Fields["stdout"] = v
	}
	if v, ok := dictField(dict, "stderr"); ok {
		inst.Fields["stderr"] = v
	}
	if v, ok := dictField(dict, "timedOut"); ok {
		inst.Fields["timedOut"] = v
	}
	return inst, nil
}

func (e *Evaluator) httpClientObjectClasses() []*runtime.Class {
	resolveURL := func(base, rel string) string {
		if base == "" || strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
			return rel
		}
		return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
	}

	applyDefaultHeaders := func(h *httpClientHandle, req *http.Request) {
		applyDefaultUserAgent(req)
		for key, vals := range h.headers {
			for i, v := range vals {
				if i == 0 {
					req.Header.Set(key, v)
				} else {
					req.Header.Add(key, v)
				}
			}
		}
	}

	getClientHandle := func(this *runtime.Instance) (*httpClientHandle, error) {
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("invalid client handle")
		}
		e.httpClientMu.Lock()
		defer e.httpClientMu.Unlock()
		h, ok := e.httpClientHandles[id.Value.Int64()]
		if !ok {
			return nil, fmt.Errorf("client handle not found")
		}
		return h, nil
	}

	newJarInst := func(id int64) *runtime.Instance {
		return &runtime.Instance{
			Class:  e.httpCookieJarClass,
			Fields: map[string]runtime.Value{"handle": runtime.NewInt64(id)},
		}
	}

	// CookieJar class
	cookieJarClass := &runtime.Class{
		Name:    "CookieJar",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
	}
	cookieJarClass.Methods["cookies"] = []runtime.Function{{Name: "cookies", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("CookieJar.cookies expects url argument")
		}
		urlStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("CookieJar.cookies url must be string")
		}
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("invalid jar handle")
		}
		e.httpCookieJarMu.Lock()
		jar, ok := e.httpCookieJars[id.Value.Int64()]
		e.httpCookieJarMu.Unlock()
		if !ok {
			return nil, fmt.Errorf("jar handle not found")
		}
		parsedURL, err := neturl.Parse(urlStr.Value)
		if err != nil {
			return nil, fmt.Errorf("CookieJar.cookies invalid url: %v", err)
		}
		cookies := jar.Cookies(parsedURL)
		elems := make([]runtime.Value, 0, len(cookies))
		for _, c := range cookies {
			entries := map[string]runtime.DictEntry{}
			putDict(entries, "name", runtime.String{Value: c.Name})
			putDict(entries, "value", runtime.String{Value: c.Value})
			putDict(entries, "domain", runtime.String{Value: c.Domain})
			putDict(entries, "path", runtime.String{Value: c.Path})
			putDict(entries, "secure", runtime.Bool{Value: c.Secure})
			putDict(entries, "httpOnly", runtime.Bool{Value: c.HttpOnly})
			elems = append(elems, runtime.Dict{Entries: entries})
		}
		return &runtime.List{Elements: elems}, nil
	}}}
	cookieJarClass.Methods["setcookies"] = []runtime.Function{{Name: "setCookies", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("CookieJar.setCookies expects (url, list<dict>)")
		}
		urlStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("CookieJar.setCookies url must be string")
		}
		list, ok := args[1].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("CookieJar.setCookies expects a list of cookies")
		}
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("invalid jar handle")
		}
		e.httpCookieJarMu.Lock()
		jar, found := e.httpCookieJars[id.Value.Int64()]
		e.httpCookieJarMu.Unlock()
		if !found {
			return nil, fmt.Errorf("jar handle not found")
		}
		parsedURL, err := neturl.Parse(urlStr.Value)
		if err != nil {
			return nil, fmt.Errorf("CookieJar.setCookies invalid url: %v", err)
		}
		cookies := make([]*http.Cookie, 0, len(list.Elements))
		for i, el := range list.Elements {
			d, ok := el.(runtime.Dict)
			if !ok {
				return nil, fmt.Errorf("CookieJar.setCookies element %d is not a dict", i)
			}
			name, _ := dictStringField(d, "name")
			value, _ := dictStringField(d, "value")
			if name == "" {
				return nil, fmt.Errorf("CookieJar.setCookies element %d missing name", i)
			}
			c := &http.Cookie{Name: name, Value: value}
			if domain, ok := dictStringField(d, "domain"); ok {
				c.Domain = domain
			}
			if path, ok := dictStringField(d, "path"); ok {
				c.Path = path
			}
			if secure, ok := dictBoolField(d, "secure"); ok {
				c.Secure = secure
			}
			if httpOnly, ok := dictBoolField(d, "httpOnly"); ok {
				c.HttpOnly = httpOnly
			}
			cookies = append(cookies, c)
		}
		jar.SetCookies(parsedURL, cookies)
		return runtime.Null{}, nil
	}}}
	cookieJarClass.Methods["clear"] = []runtime.Function{{Name: "clear", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("invalid jar handle")
		}
		newJar, _ := cookiejar.New(nil)
		e.httpCookieJarMu.Lock()
		e.httpCookieJars[id.Value.Int64()] = newJar
		e.httpCookieJarMu.Unlock()
		return runtime.Null{}, nil
	}}}

	// Client class
	clientClass := &runtime.Class{
		Name:    "Client",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
	}
	clientClass.Methods["get"] = []runtime.Function{{Name: "get", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("Client.get expects url and optional headers")
		}
		urlStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.get url must be string")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodGet, resolveURL(h.baseURL, urlStr.Value), nil)
		if err != nil {
			return nil, err
		}
		applyDefaultHeaders(h, req)
		if len(args) == 2 {
			if err := applyRequestHeaders(nil, req, args[1]); err != nil {
				return nil, err
			}
		}
		return doHTTPRequest(h.client, req)
	}}}
	clientClass.Methods["post"] = []runtime.Function{{Name: "post", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("Client.post expects url, body, and optional headers")
		}
		urlStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.post url must be string")
		}
		body, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.post body must be string")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, resolveURL(h.baseURL, urlStr.Value), strings.NewReader(body.Value))
		if err != nil {
			return nil, err
		}
		applyDefaultHeaders(h, req)
		if len(args) == 3 {
			if err := applyRequestHeaders(nil, req, args[2]); err != nil {
				return nil, err
			}
		}
		return doHTTPRequest(h.client, req)
	}}}
	clientClass.Methods["request"] = []runtime.Function{{Name: "request", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Client.request expects one options dict")
		}
		opts, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("Client.request options must be dict")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		method := "GET"
		if v, ok := dictStringField(opts, "method"); ok {
			method = v
		}
		urlStr, ok := dictStringField(opts, "url")
		if !ok {
			return nil, fmt.Errorf("Client.request options.url is required")
		}
		body := ""
		if v, ok := dictStringField(opts, "body"); ok {
			body = v
		}
		req, err := http.NewRequest(method, resolveURL(h.baseURL, urlStr), strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		applyDefaultHeaders(h, req)
		if hdrs, ok := dictField(opts, "headers"); ok {
			if err := applyRequestHeaders(nil, req, hdrs); err != nil {
				return nil, err
			}
		}
		client := h.client
		if v, ok := dictField(opts, "timeoutMs"); ok {
			if ms, ok := toInt64(v); ok {
				client = &http.Client{
					Jar:       h.client.Jar,
					Transport: h.client.Transport,
					Timeout:   time.Duration(ms) * time.Millisecond,
				}
			}
		}
		retry, err := httpRetryOptionsFromDict(opts)
		if err != nil {
			return nil, err
		}
		return doHTTPRequestWithRetries(client, req, retry)
	}}}
	clientClass.Methods["settimeout"] = []runtime.Function{{Name: "setTimeout", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Client.setTimeout expects milliseconds argument")
		}
		ms, ok := toInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("Client.setTimeout argument must be int")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		h.client.Timeout = time.Duration(ms) * time.Millisecond
		return runtime.Null{}, nil
	}}}
	clientClass.Methods["setbaseurl"] = []runtime.Function{{Name: "setBaseUrl", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Client.setBaseUrl expects url argument")
		}
		urlStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.setBaseUrl argument must be string")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		h.baseURL = urlStr.Value
		return runtime.Null{}, nil
	}}}
	clientClass.Methods["setdefaultheader"] = []runtime.Function{{Name: "setDefaultHeader", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("Client.setDefaultHeader expects name and value arguments")
		}
		name, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.setDefaultHeader name must be string")
		}
		val, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.setDefaultHeader value must be string")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		h.headers.Set(name.Value, val.Value)
		return runtime.Null{}, nil
	}}}
	clientClass.Methods["attachcookiejar"] = []runtime.Function{{Name: "attachCookieJar", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Client.attachCookieJar expects a CookieJar argument")
		}
		jarInst, ok := args[0].(*runtime.Instance)
		if !ok {
			return nil, fmt.Errorf("Client.attachCookieJar argument must be a CookieJar instance")
		}
		jarIDVal, ok := jarInst.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("Client.attachCookieJar invalid jar instance")
		}
		e.httpCookieJarMu.Lock()
		jar, ok := e.httpCookieJars[jarIDVal.Value.Int64()]
		e.httpCookieJarMu.Unlock()
		if !ok {
			return nil, fmt.Errorf("Client.attachCookieJar jar not found")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		h.client.Jar = jar
		return runtime.Null{}, nil
	}}}
	clientClass.Methods["cookiejar"] = []runtime.Function{{Name: "cookieJar", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		e.httpCookieJarMu.Lock()
		defer e.httpCookieJarMu.Unlock()
		for id, jar := range e.httpCookieJars {
			if jar == h.client.Jar {
				return newJarInst(id), nil
			}
		}
		jar, _ := cookiejar.New(nil)
		h.client.Jar = jar
		id := e.nextCookieJarID
		e.nextCookieJarID++
		e.httpCookieJars[id] = jar
		return newJarInst(id), nil
	}}}

	// Helpers for async and batch operations
	doHTTPRequestAsync := func(client *http.Client, req *http.Request) *runtime.Task {
		task := runtime.NewTask()
		go func() {
			result, err := doHTTPRequest(client, req)
			task.Complete(result, err)
		}()
		return task
	}

	buildRequestFromSpec := func(h *httpClientHandle, spec runtime.Dict) (*http.Request, string, error) {
		method := "GET"
		if v, ok := dictStringField(spec, "method"); ok {
			method = strings.ToUpper(v)
		}
		urlStr, ok := dictStringField(spec, "url")
		if !ok {
			return nil, "", fmt.Errorf("request spec missing url")
		}
		body := ""
		if v, ok := dictStringField(spec, "body"); ok {
			body = v
		}
		resolvedURL := urlStr
		if h != nil {
			resolvedURL = resolveURL(h.baseURL, urlStr)
		}
		req, err := http.NewRequest(method, resolvedURL, strings.NewReader(body))
		if err != nil {
			return nil, resolvedURL, err
		}
		if h != nil {
			applyDefaultHeaders(h, req)
		}
		if hdrs, ok := dictField(spec, "headers"); ok {
			if err := applyRequestHeaders(nil, req, hdrs); err != nil {
				return nil, resolvedURL, err
			}
		}
		return req, resolvedURL, nil
	}

	clientClass.Methods["getasync"] = []runtime.Function{{Name: "getAsync", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("Client.getAsync expects url and optional headers")
		}
		urlStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.getAsync url must be string")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodGet, resolveURL(h.baseURL, urlStr.Value), nil)
		if err != nil {
			return nil, err
		}
		applyDefaultHeaders(h, req)
		if len(args) == 2 {
			if err := applyRequestHeaders(nil, req, args[1]); err != nil {
				return nil, err
			}
		}
		return doHTTPRequestAsync(h.client, req), nil
	}}}
	clientClass.Methods["postasync"] = []runtime.Function{{Name: "postAsync", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("Client.postAsync expects url, body, and optional headers")
		}
		urlStr, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.postAsync url must be string")
		}
		body, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Client.postAsync body must be string")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, resolveURL(h.baseURL, urlStr.Value), strings.NewReader(body.Value))
		if err != nil {
			return nil, err
		}
		applyDefaultHeaders(h, req)
		if len(args) == 3 {
			if err := applyRequestHeaders(nil, req, args[2]); err != nil {
				return nil, err
			}
		}
		return doHTTPRequestAsync(h.client, req), nil
	}}}
	clientClass.Methods["requestasync"] = []runtime.Function{{Name: "requestAsync", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Client.requestAsync expects one options dict")
		}
		opts, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("Client.requestAsync options must be dict")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		req, _, err := buildRequestFromSpec(h, opts)
		if err != nil {
			return nil, err
		}
		client := h.client
		if v, ok := dictField(opts, "timeoutMs"); ok {
			if ms, ok := toInt64(v); ok {
				client = &http.Client{
					Jar:       h.client.Jar,
					Transport: h.client.Transport,
					Timeout:   time.Duration(ms) * time.Millisecond,
				}
			}
		}
		return doHTTPRequestAsync(client, req), nil
	}}}

	// FetchStream class - completion-ordered streaming parallel fetch
	fetchStreamClass := &runtime.Class{
		Name:    "FetchStream",
		Fields:  []runtime.Field{{Name: "handle"}},
		Methods: map[string][]runtime.Function{},
	}

	getFetchStreamHandle := func(this *runtime.Instance) (*httpFetchStreamHandle, error) {
		id, ok := this.Fields["handle"].(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("invalid fetch stream handle")
		}
		e.httpFetchStreamMu.Lock()
		defer e.httpFetchStreamMu.Unlock()
		sh, ok := e.httpFetchStreams[id.Value.Int64()]
		if !ok {
			return nil, fmt.Errorf("fetch stream handle not found")
		}
		return sh, nil
	}

	nextFn := func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		sh, err := getFetchStreamHandle(this)
		if err != nil {
			return nil, err
		}
		sh.mu.Lock()
		if sh.read >= sh.total {
			sh.mu.Unlock()
			return runtime.Null{}, nil
		}
		sh.mu.Unlock()
		result := <-sh.ch
		sh.mu.Lock()
		sh.read++
		sh.mu.Unlock()
		return result, nil
	}
	fetchStreamClass.Methods["next"] = []runtime.Function{{Name: "next", Native: nextFn}}
	fetchStreamClass.Methods["nextasync"] = []runtime.Function{{Name: "nextAsync", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		task := runtime.NewTask()
		go func() {
			result, err := nextFn(this, args)
			task.Complete(result, err)
		}()
		return task, nil
	}}}
	fetchStreamClass.Methods["remaining"] = []runtime.Function{{Name: "remaining", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		sh, err := getFetchStreamHandle(this)
		if err != nil {
			return nil, err
		}
		sh.mu.Lock()
		defer sh.mu.Unlock()
		return runtime.NewInt64(int64(sh.total - sh.read)), nil
	}}}
	fetchStreamClass.Methods["done"] = []runtime.Function{{Name: "done", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		sh, err := getFetchStreamHandle(this)
		if err != nil {
			return nil, err
		}
		sh.mu.Lock()
		defer sh.mu.Unlock()
		return runtime.Bool{Value: sh.read >= sh.total}, nil
	}}}

	spawnFetchStream := func(h *httpClientHandle, specs []runtime.Value) (*runtime.Instance, error) {
		ch := make(chan runtime.Value, len(specs))
		sh := &httpFetchStreamHandle{ch: ch, total: len(specs)}
		e.httpFetchStreamMu.Lock()
		id := e.nextFetchStreamID
		e.nextFetchStreamID++
		e.httpFetchStreams[id] = sh
		e.httpFetchStreamMu.Unlock()
		for i, specVal := range specs {
			go func(idx int, sv runtime.Value) {
				d, ok := sv.(runtime.Dict)
				if !ok {
					entries := map[string]runtime.DictEntry{}
					putDict(entries, "error", runtime.String{Value: "request spec must be a dict"})
					putDict(entries, "index", runtime.NewInt64(int64(idx)))
					ch <- runtime.Dict{Entries: entries}
					return
				}
				req, resolvedURL, reqErr := buildRequestFromSpec(h, d)
				if reqErr != nil {
					entries := map[string]runtime.DictEntry{}
					putDict(entries, "error", runtime.String{Value: reqErr.Error()})
					putDict(entries, "index", runtime.NewInt64(int64(idx)))
					putDict(entries, "url", runtime.String{Value: resolvedURL})
					ch <- runtime.Dict{Entries: entries}
					return
				}
				var client *http.Client
				if h != nil {
					client = h.client
				} else {
					client = http.DefaultClient
				}
				result, doErr := doHTTPRequest(client, req)
				if doErr != nil {
					entries := map[string]runtime.DictEntry{}
					putDict(entries, "error", runtime.String{Value: doErr.Error()})
					putDict(entries, "index", runtime.NewInt64(int64(idx)))
					putDict(entries, "url", runtime.String{Value: resolvedURL})
					ch <- runtime.Dict{Entries: entries}
					return
				}
				if dict, ok := result.(runtime.Dict); ok {
					putDict(dict.Entries, "index", runtime.NewInt64(int64(idx)))
					putDict(dict.Entries, "url", runtime.String{Value: resolvedURL})
					ch <- dict
				} else {
					ch <- result
				}
			}(i, specVal)
		}
		return &runtime.Instance{Class: fetchStreamClass, Fields: map[string]runtime.Value{"handle": runtime.NewInt64(id)}}, nil
	}

	clientClass.Methods["fetchall"] = []runtime.Function{{Name: "fetchAll", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Client.fetchAll expects a list of request specs")
		}
		list, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("Client.fetchAll argument must be a list")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		specs := list.Elements
		task := runtime.NewTask()
		go func() {
			results := make([]runtime.Value, len(specs))
			var wg sync.WaitGroup
			for i, sv := range specs {
				wg.Add(1)
				go func(idx int, specVal runtime.Value) {
					defer wg.Done()
					d, ok := specVal.(runtime.Dict)
					if !ok {
						entries := map[string]runtime.DictEntry{}
						putDict(entries, "error", runtime.String{Value: "request spec must be a dict"})
						results[idx] = runtime.Dict{Entries: entries}
						return
					}
					req, _, reqErr := buildRequestFromSpec(h, d)
					if reqErr != nil {
						entries := map[string]runtime.DictEntry{}
						putDict(entries, "error", runtime.String{Value: reqErr.Error()})
						results[idx] = runtime.Dict{Entries: entries}
						return
					}
					result, doErr := doHTTPRequest(h.client, req)
					if doErr != nil {
						entries := map[string]runtime.DictEntry{}
						putDict(entries, "error", runtime.String{Value: doErr.Error()})
						results[idx] = runtime.Dict{Entries: entries}
						return
					}
					results[idx] = result
				}(i, sv)
			}
			wg.Wait()
			task.Complete(&runtime.List{Elements: results}, nil)
		}()
		return task, nil
	}}}
	clientClass.Methods["fetchstream"] = []runtime.Function{{Name: "fetchStream", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Client.fetchStream expects a list of request specs")
		}
		list, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("Client.fetchStream argument must be a list")
		}
		h, err := getClientHandle(this)
		if err != nil {
			return nil, err
		}
		return spawnFetchStream(h, list.Elements)
	}}}

	// RequestBuilder class
	builderClass := &runtime.Class{
		Name: "Builder",
		Fields: []runtime.Field{
			{Name: "_url"}, {Name: "_method"}, {Name: "_body"},
			{Name: "_timeoutMs"}, {Name: "_headers"},
		},
		Methods: map[string][]runtime.Function{},
	}
	builderClass.Methods["method"] = []runtime.Function{{Name: "method", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Builder.method expects one argument")
		}
		m, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Builder.method argument must be string")
		}
		this.Fields["_method"] = m
		return this, nil
	}}}
	builderClass.Methods["header"] = []runtime.Function{{Name: "header", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("Builder.header expects name and value arguments")
		}
		name, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Builder.header name must be string")
		}
		val, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Builder.header value must be string")
		}
		var hdrs runtime.HTTPHeaders
		if existing, ok := this.Fields["_headers"].(runtime.HTTPHeaders); ok {
			hdrs = existing
		} else {
			hdrs = runtime.HTTPHeaders{Values: map[string][]string{}}
		}
		hdrs.Values[http.CanonicalHeaderKey(name.Value)] = []string{val.Value}
		this.Fields["_headers"] = hdrs
		return this, nil
	}}}
	builderClass.Methods["body"] = []runtime.Function{{Name: "body", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Builder.body expects one argument")
		}
		b, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Builder.body argument must be string")
		}
		this.Fields["_body"] = b
		return this, nil
	}}}
	builderClass.Methods["timeout"] = []runtime.Function{{Name: "timeout", Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Builder.timeout expects milliseconds argument")
		}
		if !native.IsInt(args[0]) {
			return nil, fmt.Errorf("Builder.timeout argument must be int")
		}
		this.Fields["_timeoutMs"] = args[0]
		return this, nil
	}}}
	builderClass.Methods["send"] = []runtime.Function{{Name: "send", Native: func(this *runtime.Instance, _ []runtime.Value) (runtime.Value, error) {
		urlVal, ok := this.Fields["_url"].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Builder.send: url is not set")
		}
		method := "GET"
		if m, ok := this.Fields["_method"].(runtime.String); ok {
			method = m.Value
		}
		body := ""
		if b, ok := this.Fields["_body"].(runtime.String); ok {
			body = b.Value
		}
		req, err := http.NewRequest(method, urlVal.Value, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		if hdrs, ok := this.Fields["_headers"].(runtime.HTTPHeaders); ok {
			for key, vals := range hdrs.Values {
				for i, v := range vals {
					if i == 0 {
						req.Header.Set(key, v)
					} else {
						req.Header.Add(key, v)
					}
				}
			}
		}
		client := http.DefaultClient
		if msField, ok := this.Fields["_timeoutMs"]; ok {
			if ms, ok := native.AsInt64(msField); ok {
				client = &http.Client{Timeout: time.Duration(ms) * time.Millisecond}
			}
		}
		return doHTTPRequest(client, req)
	}}}

	return []*runtime.Class{cookieJarClass, clientClass, builderClass, fetchStreamClass}
}

func streamInterfaces() []*runtime.Interface {
	return []*runtime.Interface{
		streamInterface("JsonStreamInterface", []methodSpec{
			{"onStartObject", nil},
			{"onEndObject", nil},
			{"onStartArray", nil},
			{"onEndArray", nil},
			{"onKey", []string{"key"}},
			{"onValue", []string{"value"}},
			{"onError", []string{"error"}},
		}),
		streamInterface("XmlStreamInterface", []methodSpec{
			{"onStartElement", []string{"name", "attributes"}},
			{"onEndElement", []string{"name"}},
			{"onText", []string{"text"}},
			{"onComment", []string{"text"}},
			{"onError", []string{"error"}},
		}),
		streamInterface("YamlStreamInterface", []methodSpec{
			{"onStartMap", nil},
			{"onEndMap", nil},
			{"onStartList", nil},
			{"onEndList", nil},
			{"onKey", []string{"key"}},
			{"onValue", []string{"value"}},
			{"onError", []string{"error"}},
		}),
		streamInterface("CsvStreamInterface", []methodSpec{
			{"onHeader", []string{"columns"}},
			{"onRow", []string{"row"}},
			{"onError", []string{"error"}},
		}),
		streamInterface("LogInterface", []methodSpec{
			{"handle", []string{"level", "message", "fields"}},
		}),
	}
}

type methodSpec struct {
	name       string
	parameters []string
}

func streamInterface(name string, methods []methodSpec) *runtime.Interface {
	iface := &runtime.Interface{Name: name}
	for _, method := range methods {
		sig := &ast.FunctionSignature{Name: &ast.Identifier{Value: method.name}}
		for _, param := range method.parameters {
			sig.Parameters = append(sig.Parameters, ast.Parameter{Name: &ast.Identifier{Value: param}})
		}
		iface.Methods = append(iface.Methods, sig)
	}
	return iface
}

func buildEnum(stmt *ast.EnumStatement) *runtime.EnumDef {
	enum := &runtime.EnumDef{Name: stmt.Name.Value}
	for _, v := range stmt.Variants {
		enum.Variants = append(enum.Variants, runtime.EnumVariantDefRuntime{
			Name:       v.Name.Value,
			FieldCount: len(v.FieldTypes),
		})
	}
	return enum
}

func enumVariantValue(enum *runtime.EnumDef, name string) (runtime.Value, error) {
	for _, variant := range enum.Variants {
		if !strings.EqualFold(variant.Name, name) {
			continue
		}
		if variant.FieldCount == 0 {
			return runtime.EnumVariant{Enum: enum, Variant: variant.Name}, nil
		}
		capturedEnum := enum
		capturedName := variant.Name
		return runtime.Function{
			Name: enum.Name + "." + variant.Name,
			Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
				return runtime.EnumVariant{Enum: capturedEnum, Variant: capturedName, Fields: args}, nil
			},
		}, nil
	}
	return nil, fmt.Errorf("enum %s has no variant %s", enum.Name, name)
}

func enumVariantField(ev runtime.EnumVariant, name string) (runtime.Value, error) {
	switch name {
	case "variant":
		return runtime.String{Value: ev.Variant}, nil
	case "fields":
		return &runtime.List{Elements: ev.Fields}, nil
	}
	return nil, fmt.Errorf("enum variant %s.%s has no field %s", ev.Enum.Name, ev.Variant, name)
}

func (e *Evaluator) buildClass(stmt *ast.ClassStatement, env *runtime.Environment) (*runtime.Class, error) {
	classTypeParams := typeParameterNames(stmt.Generics)
	classTypeParamConstraints := typeParamConstraints(stmt.Generics)
	class := &runtime.Class{
		Name:                 stmt.Name.Value,
		Doc:                  stmt.Doc,
		TypeParameters:       classTypeParams,
		TypeParamConstraints: classTypeParamConstraints,
		Decorators:           stmt.Decorators,
		Fields:               []runtime.Field{},
		Methods:              map[string][]runtime.Function{},
		StaticMethods:        map[string][]runtime.Function{},
		StaticValues:         map[string]runtime.Value{},
		Env:                  env,
		DefinitionLine:       stmt.Token.Line,
		DefinitionColumn:     stmt.Token.Column,
	}
	if stmt.Extends != nil {
		if isBuiltinErrorClass(stmt.Extends.Name) {
			class.Parent = e.getErrorSentinel(stmt.Extends.Name)
			e.errorClassParents[class.Name] = stmt.Extends.Name
		} else {
			parentValue, ok, err := e.resolveTypeValue(stmt.Extends, env)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("parent class %q is not declared", stmt.Extends.Name)
			}
			parent, ok := parentValue.(*runtime.Class)
			if !ok {
				return nil, fmt.Errorf("%q is not a class", stmt.Extends.Name)
			}
			class.Parent = parent
			if len(stmt.Extends.Arguments) > 0 {
				args := make([]string, 0, len(stmt.Extends.Arguments))
				for _, arg := range stmt.Extends.Arguments {
					args = append(args, arg.String())
				}
				class.ParentArguments = args
			}
			if isErrorDerived(class) {
				e.errorClassParents[class.Name] = parent.Name
			}
		}
	}
	implementedIfaces := make([]*runtime.Interface, 0, len(stmt.Implements))
	for _, ifaceRef := range stmt.Implements {
		ifaceValue, ok, err := e.resolveTypeValue(ifaceRef, env)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("interface %q is not declared", ifaceRef.Name)
		}
		iface, ok := ifaceValue.(*runtime.Interface)
		if !ok {
			return nil, fmt.Errorf("%q is not an interface", ifaceRef.Name)
		}
		class.Implements = append(class.Implements, iface)
		implementedIfaces = append(implementedIfaces, iface)
	}
	if err := mergeInterfaceMembers(stmt, implementedIfaces); err != nil {
		return nil, err
	}
	for _, member := range stmt.Members {
		switch member := member.(type) {
		case *ast.DeclarationStatement:
			if member.Name == nil {
				continue
			}
			if member.Kind == "static const" || member.Kind == "static let" {
				value := runtime.Value(runtime.Null{})
				if member.Value != nil {
					evaluated, err := e.evalExpression(member.Value, env)
					if err != nil {
						return nil, err
					}
					value = evaluated
				}
				class.StaticValues[member.Name.Value] = value
				continue
			}
			if strings.HasPrefix(member.Kind, "static ") {
				return nil, fmt.Errorf("only static const and static let class members are supported")
			}
			class.Fields = append(class.Fields, runtime.Field{Name: member.Name.Value, Type: member.Type, Default: member.Value, Decorators: member.Decorators})
		case *ast.FunctionStatement:
			target := "method"
			if member.Static {
				target = "staticMethod"
			}
			methodTypeParams := append(typeParameterNames(member.Generics), classTypeParams...)
			fn := runtime.Function{Name: member.Name.Value, Doc: member.Doc, TypeParameters: methodTypeParams, TypeParamConstraints: mergeTypeParamConstraints(typeParamConstraints(member.Generics), classTypeParamConstraints), Parameters: e.resolveParameters(member.Parameters), ReturnType: e.resolveTypeRef(member.ReturnType), Body: member.Body, Env: env, Decorators: member.Decorators, Target: target, Async: member.Async, IsGenerator: blockContainsYield(member.Body), ForwardThis: !member.Static}
			decorated, err := e.applyCallableFunctionDecorators(fn, member.Decorators, env)
			if err != nil {
				return nil, err
			}
			fn = decorated
			fn.OwnerClass = class
			if member.Static {
				key := strings.ToLower(member.Name.Value)
				class.StaticMethods[key] = append(class.StaticMethods[key], fn)
				continue
			}
			if strings.EqualFold(member.Name.Value, stmt.Name.Value) {
				class.Constructors = append(class.Constructors, fn)
			} else {
				key := strings.ToLower(member.Name.Value)
				class.Methods[key] = append(class.Methods[key], fn)
			}
		default:
			return nil, fmt.Errorf("unsupported class member %T", member)
		}
	}
	if stmt.Destructor != nil {
		dtor := stmt.Destructor
		methodTypeParams := append(typeParameterNames(dtor.Generics), classTypeParams...)
		fn := runtime.Function{Name: dtor.Name.Value, Doc: dtor.Doc, TypeParameters: methodTypeParams, TypeParamConstraints: mergeTypeParamConstraints(typeParamConstraints(dtor.Generics), classTypeParamConstraints), Parameters: e.resolveParameters(dtor.Parameters), ReturnType: e.resolveTypeRef(dtor.ReturnType), Body: dtor.Body, Env: env, Decorators: dtor.Decorators, Target: "method", Async: dtor.Async, IsGenerator: false, ForwardThis: true}
		decorated, err := e.applyCallableFunctionDecorators(fn, dtor.Decorators, env)
		if err != nil {
			return nil, err
		}
		fn = decorated
		fn.OwnerClass = class
		class.Destructor = &fn
	}
	for _, iface := range class.Implements {
		if err := validateInterfaceImplementation(class, iface); err != nil {
			return nil, err
		}
	}
	return class, nil
}

func (e *Evaluator) resolveTypeValue(ref *ast.TypeRef, env *runtime.Environment) (runtime.Value, bool, error) {
	if ref == nil || ref.Operator != "" {
		return nil, false, nil
	}
	if moduleName, exportName, ok := strings.Cut(ref.Name, "."); ok {
		moduleValue, exists := env.Get(moduleName)
		if !exists {
			return nil, false, nil
		}
		module, ok := moduleValue.(*runtime.Module)
		if !ok {
			return nil, false, fmt.Errorf("%s is not a module", moduleName)
		}
		value, exists := module.Exports[exportName]
		return value, exists, nil
	}
	value, ok := env.Get(ref.Name)
	return value, ok, nil
}

func isErrorDerived(class *runtime.Class) bool {
	for c := class; c != nil; c = c.Parent {
		if isBuiltinErrorClass(c.Name) {
			return true
		}
	}
	return false
}

func (e *Evaluator) instantiateClass(class *runtime.Class, args []runtime.Value) (runtime.Value, error) {
	if reason, abstract := classAbstractnessReason(class); abstract {
		return nil, thrownError{value: e.withTrace(runtime.Error{Class: "RuntimeError", Message: reason, Parents: []string{"RuntimeError", "Error"}})}
	}
	if isErrorDerived(class) {
		if len(class.Fields) > 0 || len(class.Constructors) > 0 {
			return e.instantiateUserErrorClass(class, args)
		}
		msg := ""
		for _, arg := range args {
			if s, ok := arg.(runtime.String); ok {
				msg = s.Value
				break
			}
		}
		return runtime.Error{Class: class.Name, Message: msg, Parents: e.errorParentChain(class.Name)}, nil
	}
	instance := &runtime.Instance{Class: class, Fields: map[string]runtime.Value{}}
	if err := e.initializeFields(instance, class); err != nil {
		return nil, err
	}
	if len(class.Constructors) > 0 {
		constructor, err := selectOverload(class.Name, class.Constructors, args)
		if err != nil {
			return nil, err
		}
		if err := e.applyAutoParentConstructor(instance, constructor); err != nil {
			return nil, err
		}
		if _, err := e.applyFunctionWithThis(constructor, args, instance); err != nil {
			return nil, err
		}
	} else if len(args) != 0 {
		return nil, fmt.Errorf("%s constructor expects no arguments", class.Name)
	}
	if err := e.checkClassTypeParamConstraints(class, instance.TypeBindings); err != nil {
		return nil, err
	}
	if class.Immutable {
		instance.Frozen = true
	}
	if class.Destructor != nil {
		e.destructibleInstances = append(e.destructibleInstances, instance)
	}
	return instance, nil
}

// instantiateUserErrorClass runs the full constructor path for user-defined error
// subclasses that declare custom fields or a constructor. After construction, the
// instance fields (minus the __parentMsg sentinel) are preserved in Error.Fields.
func (e *Evaluator) instantiateUserErrorClass(class *runtime.Class, args []runtime.Value) (runtime.Value, error) {
	instance := &runtime.Instance{Class: class, Fields: map[string]runtime.Value{}}
	if err := e.initializeFields(instance, class); err != nil {
		return nil, err
	}
	if len(class.Constructors) > 0 {
		constructor, err := selectOverload(class.Name, class.Constructors, args)
		if err != nil {
			return nil, err
		}
		if _, err := e.applyFunctionWithThis(constructor, args, instance); err != nil {
			return nil, err
		}
	} else if len(args) != 0 {
		return nil, fmt.Errorf("%s constructor expects no arguments", class.Name)
	}
	msg := ""
	if m, ok := instance.Fields["__parentMsg"]; ok {
		if s, ok := m.(runtime.String); ok {
			msg = s.Value
		}
		delete(instance.Fields, "__parentMsg")
	} else {
		for _, arg := range args {
			if s, ok := arg.(runtime.String); ok {
				msg = s.Value
				break
			}
		}
	}
	var fields map[string]runtime.Value
	if len(instance.Fields) > 0 {
		fields = instance.Fields
	}
	return runtime.Error{Class: class.Name, Message: msg, Fields: fields, Parents: e.errorParentChain(class.Name)}, nil
}

// registerGlobalClass adds a class to the cross-module registry so
// reflect.class(name) can find it from any module. Idempotent: classes
// re-imported across modules just overwrite the entry with the same
// pointer (Geblang class identity is global by name).
func (e *Evaluator) registerGlobalClass(class *runtime.Class) {
	if class == nil || class.Name == "" {
		return
	}
	e.globalClasses[class.Name] = class
}

// errorParentChain returns the parent class name list (immediate parent
// first) for an error-derived class, used when constructing a
// runtime.Error value so cross-module `instanceof` / typed-parameter
// matching can walk the chain without re-reading the evaluator state.
func (e *Evaluator) errorParentChain(className string) []string {
	var parents []string
	visited := map[string]bool{className: true}
	for parent := e.errorParent(className); parent != ""; parent = e.errorParent(parent) {
		if visited[parent] {
			break
		}
		visited[parent] = true
		parents = append(parents, parent)
	}
	return parents
}

func (e *Evaluator) instantiateClassFromCall(class *runtime.Class, call *ast.CallExpression, env *runtime.Environment, declared ...*ast.TypeRef) (runtime.Value, error) {
	if reason, abstract := classAbstractnessReason(class); abstract {
		return nil, thrownError{value: e.withTrace(runtime.Error{Class: "RuntimeError", Message: reason, Parents: []string{"RuntimeError", "Error"}})}
	}
	if isErrorDerived(class) {
		if len(class.Fields) > 0 || len(class.Constructors) > 0 {
			args, err := e.evalCallArguments(call, env)
			if err != nil {
				return nil, err
			}
			return e.instantiateUserErrorClass(class, args)
		}
		msg := ""
		for _, callArg := range call.Arguments {
			argVal, err := e.evalExpression(callArg.Value, env)
			if err != nil {
				return nil, err
			}
			if s, ok := argVal.(runtime.String); ok {
				msg = s.Value
				break
			}
		}
		return runtime.Error{Class: class.Name, Message: msg, Parents: e.errorParentChain(class.Name)}, nil
	}
	instance := &runtime.Instance{Class: class, Fields: map[string]runtime.Value{}}
	// Inherit type bindings from the parent class's declaration (e.g.
	// `class Sub extends Base<string>` propagates {T: "string"} through
	// the parent chain). Done first so explicit declaration annotations
	// and constructor-argument inference below can override.
	for c := class; c != nil; c = c.Parent {
		if c.Parent == nil || len(c.ParentArguments) == 0 || len(c.Parent.TypeParameters) == 0 {
			continue
		}
		if instance.TypeBindings == nil {
			instance.TypeBindings = map[string]string{}
		}
		for i, name := range c.Parent.TypeParameters {
			if i >= len(c.ParentArguments) {
				break
			}
			if _, exists := instance.TypeBindings[name]; !exists {
				instance.TypeBindings[name] = c.ParentArguments[i]
			}
		}
	}
	// Pre-populate type bindings from the call site's explicit type arguments
	// (e.g. `Repository<Dog>(...)`); failing that, fall back to the declared
	// LHS annotation (e.g. `Box<int> b = Box(...)`). Either path takes
	// priority over inference from constructor args, which fills in any
	// remaining bindings further down.
	if len(call.TypeArguments) > 0 && len(class.TypeParameters) > 0 {
		if instance.TypeBindings == nil {
			instance.TypeBindings = map[string]string{}
		}
		for i, arg := range call.TypeArguments {
			if i >= len(class.TypeParameters) {
				break
			}
			if arg != nil && arg.Operator == "" && arg.Name != "" {
				instance.TypeBindings[class.TypeParameters[i]] = arg.Name
			}
		}
	} else if len(declared) > 0 && declared[0] != nil {
		exp := declared[0]
		if exp.Operator == "" && len(exp.Arguments) > 0 && len(class.TypeParameters) > 0 {
			if instance.TypeBindings == nil {
				instance.TypeBindings = map[string]string{}
			}
			for i, arg := range exp.Arguments {
				if i >= len(class.TypeParameters) {
					break
				}
				if arg != nil && arg.Operator == "" && arg.Name != "" {
					instance.TypeBindings[class.TypeParameters[i]] = arg.Name
				}
			}
		}
	}
	if err := e.initializeFields(instance, class); err != nil {
		return nil, err
	}
	if len(class.Constructors) == 0 {
		if len(call.Arguments) != 0 {
			return nil, fmt.Errorf("%s constructor expects no arguments", class.Name)
		}
		if err := e.checkClassTypeParamConstraints(class, instance.TypeBindings); err != nil {
			return nil, err
		}
		if class.Immutable {
			instance.Frozen = true
		}
		if class.Destructor != nil {
			e.destructibleInstances = append(e.destructibleInstances, instance)
		}
		return instance, nil
	}
	if _, err := e.applyOverloadedFunction(class.Name, class.Constructors, call, env, instance, nil); err != nil {
		return nil, err
	}
	if err := e.checkClassTypeParamConstraints(class, instance.TypeBindings); err != nil {
		return nil, err
	}
	if class.Immutable {
		instance.Frozen = true
	}
	if class.Destructor != nil {
		e.destructibleInstances = append(e.destructibleInstances, instance)
	}
	return instance, nil
}

func (e *Evaluator) applyAutoParentConstructor(instance *runtime.Instance, constructor runtime.Function) error {
	if instance == nil || instance.Class == nil || instance.Class.Parent == nil || len(instance.Class.Parent.Constructors) == 0 {
		return nil
	}
	if evaluatorContainsParentConstructorCall(constructor.Body) {
		return nil
	}
	parentConstructor, err := selectOverload(instance.Class.Parent.Name, instance.Class.Parent.Constructors, nil)
	if err != nil {
		return err
	}
	if err := e.applyAutoParentConstructor(&runtime.Instance{Class: instance.Class.Parent, Fields: instance.Fields, TypeBindings: instance.TypeBindings}, parentConstructor); err != nil {
		return err
	}
	_, err = e.applyFunctionWithThis(parentConstructor, nil, instance)
	return err
}

func evaluatorContainsParentConstructorCall(block *ast.BlockStatement) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Statements {
		if evaluatorStatementContainsParentConstructorCall(stmt) {
			return true
		}
	}
	return false
}

func evaluatorStatementContainsParentConstructorCall(stmt ast.Statement) bool {
	switch stmt := stmt.(type) {
	case *ast.BlockStatement:
		return evaluatorContainsParentConstructorCall(stmt)
	case *ast.ExportStatement:
		return evaluatorStatementContainsParentConstructorCall(stmt.Statement)
	case *ast.DeclarationStatement:
		return evaluatorExpressionContainsParentConstructorCall(stmt.Value)
	case *ast.ExpressionStatement:
		return evaluatorExpressionContainsParentConstructorCall(stmt.Expression)
	case *ast.ReturnStatement:
		return evaluatorExpressionContainsParentConstructorCall(stmt.Value)
	case *ast.YieldStatement:
		return evaluatorExpressionContainsParentConstructorCall(stmt.Value)
	case *ast.SimpleStatement:
		return evaluatorExpressionContainsParentConstructorCall(stmt.Value)
	case *ast.IfStatement:
		if evaluatorExpressionContainsParentConstructorCall(stmt.Condition) || evaluatorContainsParentConstructorCall(stmt.Consequence) || evaluatorContainsParentConstructorCall(stmt.Alternative) {
			return true
		}
		for _, elseif := range stmt.ElseIfs {
			if evaluatorExpressionContainsParentConstructorCall(elseif.Condition) || evaluatorContainsParentConstructorCall(elseif.Body) {
				return true
			}
		}
	case *ast.WhileStatement:
		return evaluatorExpressionContainsParentConstructorCall(stmt.Condition) || evaluatorContainsParentConstructorCall(stmt.Body)
	case *ast.ForStatement:
		return evaluatorStatementContainsParentConstructorCall(stmt.Init) ||
			evaluatorExpressionContainsParentConstructorCall(stmt.Condition) ||
			evaluatorStatementContainsParentConstructorCall(stmt.Update) ||
			evaluatorExpressionContainsParentConstructorCall(stmt.Iterable) ||
			evaluatorExpressionContainsParentConstructorCall(stmt.Step) ||
			evaluatorContainsParentConstructorCall(stmt.Body)
	case *ast.MatchStatement:
		if evaluatorExpressionContainsParentConstructorCall(stmt.Expr) {
			return true
		}
		for _, matchCase := range stmt.Cases {
			if evaluatorExpressionContainsParentConstructorCall(matchCase.Pattern) ||
				evaluatorExpressionContainsParentConstructorCall(matchCase.Guard) ||
				evaluatorExpressionContainsParentConstructorCall(matchCase.Value) ||
				evaluatorContainsParentConstructorCall(matchCase.Body) {
				return true
			}
		}
	case *ast.TryStatement:
		if evaluatorContainsParentConstructorCall(stmt.Body) || evaluatorContainsParentConstructorCall(stmt.Finally) {
			return true
		}
		for _, catch := range stmt.Catches {
			if evaluatorContainsParentConstructorCall(catch.Body) {
				return true
			}
		}
	}
	return false
}

func evaluatorExpressionContainsParentConstructorCall(expr ast.Expression) bool {
	switch expr := expr.(type) {
	case nil:
		return false
	case *ast.CallExpression:
		if ident, ok := expr.Callee.(*ast.Identifier); ok && strings.EqualFold(ident.Value, "parent") {
			return true
		}
		if evaluatorExpressionContainsParentConstructorCall(expr.Callee) {
			return true
		}
		for _, arg := range expr.Arguments {
			if evaluatorExpressionContainsParentConstructorCall(arg.Value) {
				return true
			}
		}
	case *ast.PrefixExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Right)
	case *ast.PostfixExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Left)
	case *ast.InfixExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Left) || evaluatorExpressionContainsParentConstructorCall(expr.Right)
	case *ast.AssignmentExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Left) || evaluatorExpressionContainsParentConstructorCall(expr.Value)
	case *ast.SelectorExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Object)
	case *ast.IndexExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Left) || evaluatorExpressionContainsParentConstructorCall(expr.Index)
	case *ast.ListLiteral:
		for _, element := range expr.Elements {
			if evaluatorExpressionContainsParentConstructorCall(element) {
				return true
			}
		}
	case *ast.DictLiteral:
		for _, entry := range expr.Entries {
			if evaluatorExpressionContainsParentConstructorCall(entry.Key) || evaluatorExpressionContainsParentConstructorCall(entry.Value) {
				return true
			}
		}
	case *ast.SetLiteral:
		for _, element := range expr.Elements {
			if evaluatorExpressionContainsParentConstructorCall(element) {
				return true
			}
		}
	case *ast.RangeExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Start) ||
			evaluatorExpressionContainsParentConstructorCall(expr.End) ||
			evaluatorExpressionContainsParentConstructorCall(expr.Step)
	case *ast.FunctionLiteral:
		return false
	case *ast.MatchExpression:
		if evaluatorExpressionContainsParentConstructorCall(expr.Expr) {
			return true
		}
		for _, matchCase := range expr.Cases {
			if evaluatorExpressionContainsParentConstructorCall(matchCase.Pattern) ||
				evaluatorExpressionContainsParentConstructorCall(matchCase.Guard) ||
				evaluatorExpressionContainsParentConstructorCall(matchCase.Value) ||
				evaluatorContainsParentConstructorCall(matchCase.Body) {
				return true
			}
		}
	case *ast.SpreadExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Value)
	case *ast.AwaitExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Value)
	case *ast.CastExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Value)
	case *ast.TernaryExpression:
		return evaluatorExpressionContainsParentConstructorCall(expr.Condition) ||
			evaluatorExpressionContainsParentConstructorCall(expr.ThenExpr) ||
			evaluatorExpressionContainsParentConstructorCall(expr.ElseExpr)
	}
	return false
}

func (e *Evaluator) applyFieldDecorators(class *runtime.Class, name string, value runtime.Value, env *runtime.Environment) (runtime.Value, error) {
	for c := class; c != nil; c = c.Parent {
		for _, field := range c.Fields {
			if !strings.EqualFold(field.Name, name) {
				continue
			}
			for i := len(field.Decorators) - 1; i >= 0; i-- {
				dec := field.Decorators[i]
				if dec.Name == nil {
					continue
				}
				decoratorValue, found := env.Get(dec.Name.Value)
				if !found && e.globalClasses != nil {
					if v, ok := envOrGlobalFunc(env, dec.Name.Value); ok {
						decoratorValue = v
						found = true
					}
				}
				if !found {
					continue
				}
				callArgs := []runtime.Value{}
				for _, arg := range dec.Arguments {
					v, err := e.evalExpression(arg.Value, env)
					if err != nil {
						return nil, fmt.Errorf("field decorator @%s: %w", dec.Name.Value, err)
					}
					callArgs = append(callArgs, v)
				}
				callArgs = append(callArgs, value)
				result, err := e.applyCallableNoCall(decoratorValue, callArgs)
				if err != nil {
					return nil, err
				}
				value = result
			}
			return value, nil
		}
	}
	return value, nil
}

func envOrGlobalFunc(env *runtime.Environment, name string) (runtime.Value, bool) {
	if v, ok := env.Get(name); ok {
		return v, true
	}
	return nil, false
}

// Apply a callable runtime.Value to args without going through an
// AST CallExpression; used by field decorators which need to invoke
// the transform from inside an assignment path.
func (e *Evaluator) applyCallableNoCall(callee runtime.Value, args []runtime.Value) (runtime.Value, error) {
	switch fn := callee.(type) {
	case runtime.Function:
		return e.applyFunction(fn, args)
	case runtime.OverloadedFunction:
		for _, overload := range fn.Overloads {
			bound, ok := bindEvaluatedFunctionCallArguments(overload, evaluatedCallArgsFromValues(args))
			if !ok || !functionArgumentsMatch(overload, bound) {
				continue
			}
			return e.applyFunction(overload, bound)
		}
		return nil, fmt.Errorf("no matching overload for %s", fn.Name)
	}
	return nil, fmt.Errorf("decorator is not callable: %s", callee.TypeName())
}

func evaluatedCallArgsFromValues(args []runtime.Value) []evaluatedCallArg {
	result := make([]evaluatedCallArg, len(args))
	for i, v := range args {
		result[i] = evaluatedCallArg{value: v}
	}
	return result
}

func (e *Evaluator) initializeFields(instance *runtime.Instance, class *runtime.Class) error {
	if class.Parent != nil {
		if err := e.initializeFields(instance, class.Parent); err != nil {
			return err
		}
	}
	fieldEnv := runtime.NewEnclosedEnvironment(class.Env)
	for _, field := range class.Fields {
		value := runtime.Value(runtime.Null{})
		if field.Default != nil {
			evaluated, err := e.evalExpression(field.Default, fieldEnv)
			if err != nil {
				return err
			}
			value = evaluated
		}
		instance.Fields[field.Name] = value
	}
	return nil
}

func lookupMethod(class *runtime.Class, name string) (runtime.Function, bool) {
	methods := lookupMethodOverloads(class, name)
	if len(methods) == 0 {
		return runtime.Function{}, false
	}
	return methods[0], true
}

func lookupMethodOverloads(class *runtime.Class, name string) []runtime.Function {
	var methods []runtime.Function
	seen := map[string]bool{}
	for current := class; current != nil; current = current.Parent {
		overloads, ok := current.Methods[strings.ToLower(name)]
		if !ok {
			continue
		}
		for _, method := range overloads {
			key := functionParameterSignatureKey(method)
			if seen[key] {
				continue
			}
			seen[key] = true
			methods = append(methods, method)
		}
	}
	return methods
}

func functionParameterSignatureKey(fn runtime.Function) string {
	parts := make([]string, 0, len(fn.Parameters))
	for _, param := range fn.Parameters {
		parts = append(parts, strings.ToLower(typeRefSignature(param.Type)))
	}
	return strings.Join(parts, ",")
}

func typeRefSignature(typ *ast.TypeRef) string {
	if typ == nil {
		return "any"
	}
	return typ.String()
}

func lookupStaticMethod(class *runtime.Class, name string) (runtime.Function, bool) {
	methods := lookupStaticMethodOverloads(class, name)
	if len(methods) == 0 {
		return runtime.Function{}, false
	}
	return methods[0], true
}

func lookupStaticMethodOverloads(class *runtime.Class, name string) []runtime.Function {
	for current := class; current != nil; current = current.Parent {
		methods, ok := current.StaticMethods[strings.ToLower(name)]
		if ok {
			return methods
		}
	}
	return nil
}

func lookupStaticValue(class *runtime.Class, name string) (runtime.Value, bool) {
	for current := class; current != nil; current = current.Parent {
		value, ok := current.StaticValues[name]
		if ok {
			return value, true
		}
	}
	return nil, false
}

func classImplementsInterface(class *runtime.Class, name string) bool {
	for current := class; current != nil; current = current.Parent {
		for _, iface := range current.Implements {
			if interfaceMatches(iface, name) {
				return true
			}
		}
	}
	return false
}

func interfaceMatches(iface *runtime.Interface, name string) bool {
	if strings.EqualFold(iface.Name, name) {
		return true
	}
	for _, parent := range iface.Parents {
		if interfaceMatches(parent, name) {
			return true
		}
	}
	return false
}

func validateInterfaceImplementation(class *runtime.Class, iface *runtime.Interface) error {
	for _, parent := range iface.Parents {
		if err := validateInterfaceImplementation(class, parent); err != nil {
			return err
		}
	}
	for _, sig := range iface.Methods {
		methods := lookupMethodOverloads(class, sig.Name.Value)
		found := false
		for _, method := range methods {
			if len(method.Parameters) == len(sig.Parameters) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("class %s implements %s but is missing compatible method %s", class.Name, iface.Name, sig.Name.Value)
		}
	}
	return nil
}

func (e *Evaluator) checkTypeParamConstraints(fn runtime.Function, callEnv *runtime.Environment) error {
	if len(fn.TypeParamConstraints) == 0 {
		return nil
	}
	for name, constraint := range fn.TypeParamConstraints {
		boundName, ok := callEnv.GetTypeBinding(name)
		if !ok {
			continue
		}
		if err := e.checkConstraintSatisfied(boundName, name, constraint, callEnv); err != nil {
			return err
		}
	}
	return nil
}

func (e *Evaluator) checkClassTypeParamConstraints(class *runtime.Class, bindings map[string]string) error {
	if class == nil || len(class.TypeParamConstraints) == 0 {
		return nil
	}
	env := class.Env
	for name, constraint := range class.TypeParamConstraints {
		boundName, ok := bindings[name]
		if !ok {
			continue
		}
		if err := e.checkConstraintSatisfied(boundName, name, constraint, env); err != nil {
			return err
		}
	}
	return nil
}

func (e *Evaluator) checkConstraintSatisfied(typeName, paramName string, constraint *ast.TypeRef, env *runtime.Environment) error {
	if constraint == nil {
		return nil
	}
	if constraint.Operator == "|" {
		leftErr := e.checkConstraintSatisfied(typeName, paramName, constraint.Left, env)
		if leftErr == nil {
			return nil
		}
		rightErr := e.checkConstraintSatisfied(typeName, paramName, constraint.Right, env)
		if rightErr == nil {
			return nil
		}
		return fmt.Errorf("type %s does not satisfy constraint for %s: %s", typeName, paramName, constraint.Left.Name+"|"+constraint.Right.Name)
	}
	if constraint.Operator == "&" {
		if err := e.checkConstraintSatisfied(typeName, paramName, constraint.Left, env); err != nil {
			return err
		}
		return e.checkConstraintSatisfied(typeName, paramName, constraint.Right, env)
	}
	// Leaf: check that typeName implements the named interface.
	ifaceName := constraint.Name
	ifaceVal, ok := env.Get(ifaceName)
	if !ok {
		return fmt.Errorf("constraint interface %q is not declared", ifaceName)
	}
	iface, ok := ifaceVal.(*runtime.Interface)
	if !ok {
		return fmt.Errorf("constraint %q is not an interface", ifaceName)
	}
	_ = iface // interface found and validated as *runtime.Interface
	classVal, ok := env.Get(typeName)
	if !ok {
		return fmt.Errorf("type %s does not implement constraint interface %s for type parameter %s", typeName, ifaceName, paramName)
	}
	class, ok := classVal.(*runtime.Class)
	if !ok {
		return fmt.Errorf("type %s does not implement constraint interface %s for type parameter %s", typeName, ifaceName, paramName)
	}
	if !classImplementsInterface(class, ifaceName) {
		return fmt.Errorf("type %s does not implement constraint interface %s for type parameter %s", typeName, ifaceName, paramName)
	}
	return nil
}

func currentInstance(env *runtime.Environment) (*runtime.Instance, error) {
	value, ok := env.Get("this")
	if !ok {
		return nil, fmt.Errorf("parent is only available inside methods")
	}
	instance, ok := value.(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("this is not an instance")
	}
	return instance, nil
}

func typeNameFromExpression(expr ast.Expression) (string, error) {
	switch expr := expr.(type) {
	case *ast.Identifier:
		return expr.Value, nil
	case *ast.SelectorExpression:
		if expr.Name.Value == "type" {
			return expr.Object.String(), nil
		}
		return expr.String(), nil
	}
	return "", fmt.Errorf("expected type name, got %s", expr.String())
}

func castDunderName(target string) string {
	switch strings.ToLower(strings.TrimPrefix(target, "?")) {
	case "string":
		return "__string"
	case "int":
		return "__int"
	case "float":
		return "__float"
	case "bool":
		return "__bool"
	case "decimal":
		return "__decimal"
	case "bytes":
		return "__bytes"
	}
	return ""
}

func checkCastDunderReturn(target string, value runtime.Value) error {
	want := strings.ToLower(strings.TrimPrefix(target, "?"))
	switch want {
	case "string":
		if _, ok := value.(runtime.String); !ok {
			return fmt.Errorf("__string must return string, got %s", value.TypeName())
		}
	case "int":
		switch value.(type) {
		case runtime.SmallInt, runtime.Int:
		default:
			return fmt.Errorf("__int must return int, got %s", value.TypeName())
		}
	case "float":
		if _, ok := value.(runtime.Float); !ok {
			return fmt.Errorf("__float must return float, got %s", value.TypeName())
		}
	case "bool":
		if _, ok := value.(runtime.Bool); !ok {
			return fmt.Errorf("__bool must return bool, got %s", value.TypeName())
		}
	case "decimal":
		if _, ok := value.(runtime.Decimal); !ok {
			return fmt.Errorf("__decimal must return decimal, got %s", value.TypeName())
		}
	case "bytes":
		if _, ok := value.(runtime.Bytes); !ok {
			return fmt.Errorf("__bytes must return bytes, got %s", value.TypeName())
		}
	}
	return nil
}

func castValue(value runtime.Value, target string) (runtime.Value, error) {
	if valueMatchesType(value, target) {
		return value, nil
	}
	/* Nullable targets accept null directly; non-null values fall
	 * through to the underlying type's cast logic. */
	if strings.HasPrefix(target, "?") {
		if _, isNull := value.(runtime.Null); isNull {
			return runtime.Null{}, nil
		}
		return castValue(value, target[1:])
	}
	switch target {
	case "string":
		/* `bytes as string` decodes UTF-8 (errors on invalid bytes)
		 * rather than producing the hex form `value.Inspect()` returns
		 * for bytes. Other types still use `Inspect()` as the canonical
		 * string representation. */
		if v, ok := value.(runtime.Bytes); ok {
			if !utf8.Valid(v.Value) {
				return nil, fmt.Errorf("bytes value is not valid UTF-8")
			}
			return runtime.String{Value: string(v.Value)}, nil
		}
		return runtime.String{Value: value.Inspect()}, nil
	case "int":
		switch v := value.(type) {
		case runtime.String:
			return runtime.NewIntLiteral(v.Value)
		case runtime.Decimal:
			/* Truncate toward zero: matches the C/Java/Go integer-
			 * cast convention. Use big.Int division of num/den so
			 * arbitrary-precision decimals round correctly. */
			num := new(big.Int).Set(v.Value.Num())
			den := v.Value.Denom()
			q := new(big.Int).Quo(num, den)
			return runtime.Int{Value: q}, nil
		case runtime.Float:
			return runtime.NewInt64(int64(math.Trunc(v.Value))), nil
		case runtime.Bool:
			if v.Value {
				return runtime.SmallInt{Value: 1}, nil
			}
			return runtime.SmallInt{Value: 0}, nil
		}
	case "decimal":
		switch v := value.(type) {
		case runtime.SmallInt:
			return native.SmallIntToDecimal(v), nil
		case runtime.Int:
			return intToDecimal(v), nil
		case runtime.Float:
			return runtime.NewDecimalLiteral(strconv.FormatFloat(v.Value, 'g', -1, 64))
		case runtime.String:
			return runtime.NewDecimalLiteral(v.Value)
		}
	case "float":
		switch v := value.(type) {
		case runtime.SmallInt:
			return runtime.Float{Value: float64(v.Value)}, nil
		case runtime.Int:
			f, _ := new(big.Rat).SetInt(v.Value).Float64()
			return runtime.Float{Value: f}, nil
		case runtime.Decimal:
			f, _ := v.Value.Float64()
			return runtime.Float{Value: f}, nil
		case runtime.String:
			f, err := strconv.ParseFloat(v.Value, 64)
			if err != nil {
				return nil, err
			}
			return runtime.Float{Value: f}, nil
		}
	case "bool":
		switch v := value.(type) {
		case runtime.Bool:
			return v, nil
		case runtime.SmallInt:
			return runtime.Bool{Value: v.Value != 0}, nil
		case runtime.Int:
			return runtime.Bool{Value: v.Value.Sign() != 0}, nil
		case runtime.Float:
			return runtime.Bool{Value: v.Value != 0}, nil
		case runtime.Decimal:
			return runtime.Bool{Value: v.Value.Sign() != 0}, nil
		case runtime.String:
			switch v.Value {
			case "true":
				return runtime.Bool{Value: true}, nil
			case "false":
				return runtime.Bool{Value: false}, nil
			}
		case runtime.Null:
			return runtime.Bool{Value: false}, nil
		}
	case "bytes":
		/* `string as bytes` encodes UTF-8. Go strings are already UTF-8,
		 * so we copy out the underlying byte sequence. The inverse
		 * (`bytes as string`) is handled below. */
		if v, ok := value.(runtime.String); ok {
			b := make([]byte, len(v.Value))
			copy(b, v.Value)
			return runtime.Bytes{Value: b}, nil
		}
	case "list":
		/* `set as list` materializes in iteration order (the underlying
		 * map's range order; not insertion order - sets are unordered
		 * by design). To get a deterministic order, sort the result. */
		if v, ok := value.(runtime.Set); ok {
			out := make([]runtime.Value, 0, len(v.Elements))
			for _, entry := range v.Elements {
				out = append(out, entry.Value)
			}
			return &runtime.List{Elements: out}, nil
		}
	case "set":
		/* `list as set` de-duplicates. First occurrence wins; later
		 * duplicates are dropped. */
		if v, ok := value.(*runtime.List); ok {
			elements := make(map[string]runtime.SetEntry, len(v.Elements))
			for _, elem := range v.Elements {
				k := dictKey(elem)
				if _, seen := elements[k]; seen {
					continue
				}
				elements[k] = runtime.SetEntry{Value: elem}
			}
			return runtime.Set{Elements: elements}, nil
		}
	}
	return nil, fmt.Errorf("cannot cast %s to %s", value.TypeName(), target)
}

func (e *Evaluator) resolveTypeRef(ref *ast.TypeRef) *ast.TypeRef {
	if ref == nil {
		return nil
	}
	out := cloneTypeRef(ref)
	if out.Operator != "" {
		out.Left = e.resolveTypeRef(out.Left)
		out.Right = e.resolveTypeRef(out.Right)
		return out
	}
	resolved, ok := e.typeAliases[strings.ToLower(out.Name)]
	if ok {
		alias := cloneTypeRef(resolved)
		alias.Nullable = alias.Nullable || out.Nullable
		alias.ListAlias = alias.ListAlias || out.ListAlias
		return alias
	}
	for i, arg := range out.Arguments {
		out.Arguments[i] = e.resolveTypeRef(arg)
	}
	return out
}

func (e *Evaluator) resolveParameters(params []ast.Parameter) []ast.Parameter {
	out := make([]ast.Parameter, len(params))
	for i, param := range params {
		out[i] = param
		out[i].Type = e.resolveTypeRef(param.Type)
	}
	return out
}

func (e *Evaluator) resolveFunctionSignatures(sigs []*ast.FunctionSignature) []*ast.FunctionSignature {
	out := make([]*ast.FunctionSignature, len(sigs))
	for i, sig := range sigs {
		copied := *sig
		copied.Parameters = e.resolveParameters(sig.Parameters)
		copied.ReturnType = e.resolveTypeRef(sig.ReturnType)
		out[i] = &copied
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

func typeParameterNames(params []*ast.TypeParam) []string {
	names := make([]string, 0, len(params))
	for _, param := range params {
		if param != nil && param.Name != nil {
			names = append(names, param.Name.Value)
		}
	}
	return names
}

func typeParamConstraints(params []*ast.TypeParam) map[string]*ast.TypeRef {
	if len(params) == 0 {
		return nil
	}
	var m map[string]*ast.TypeRef
	for _, param := range params {
		if param != nil && param.Name != nil && param.Constraint != nil {
			if m == nil {
				m = map[string]*ast.TypeRef{}
			}
			m[param.Name.Value] = param.Constraint
		}
	}
	return m
}

func mergeTypeParamConstraints(maps ...map[string]*ast.TypeRef) map[string]*ast.TypeRef {
	var out map[string]*ast.TypeRef
	for _, constraints := range maps {
		for name, constraint := range constraints {
			if out == nil {
				out = map[string]*ast.TypeRef{}
			}
			out[name] = constraint
		}
	}
	return out
}

func blockContainsYield(block *ast.BlockStatement) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Statements {
		if statementContainsYield(stmt) {
			return true
		}
	}
	return false
}

func statementContainsYield(stmt ast.Statement) bool {
	switch stmt := stmt.(type) {
	case *ast.YieldStatement:
		return true
	case *ast.IfStatement:
		if blockContainsYield(stmt.Consequence) || blockContainsYield(stmt.Alternative) {
			return true
		}
		for _, elseif := range stmt.ElseIfs {
			if blockContainsYield(elseif.Body) {
				return true
			}
		}
	case *ast.WhileStatement:
		return blockContainsYield(stmt.Body)
	case *ast.ForStatement:
		return blockContainsYield(stmt.Body)
	case *ast.TryStatement:
		if blockContainsYield(stmt.Body) || blockContainsYield(stmt.Finally) {
			return true
		}
		for _, catch := range stmt.Catches {
			if blockContainsYield(catch.Body) {
				return true
			}
		}
	case *ast.MatchStatement:
		for _, matchCase := range stmt.Cases {
			if blockContainsYield(matchCase.Body) {
				return true
			}
		}
	}
	return false
}

func primitiveConversionTarget(name string) (string, bool) {
	switch strings.ToLower(name) {
	case "toint":
		return "int", true
	case "todecimal":
		return "decimal", true
	case "tofloat":
		return "float", true
	case "tobool":
		return "bool", true
	}
	return "", false
}

func (e *Evaluator) evalForStatement(stmt *ast.ForStatement, env *runtime.Environment) (signal, error) {
	loopEnv := runtime.NewEnclosedEnvironment(env)
	if stmt.Iterable != nil {
		return e.evalForInStatement(stmt, loopEnv)
	}
	if stmt.Init != nil {
		if _, err := e.evalStatement(stmt.Init, loopEnv); err != nil {
			return signal{}, err
		}
	}
	for {
		if stmt.Condition != nil {
			condition, err := e.evalBoolCondition(stmt.Condition, loopEnv)
			if err != nil {
				return signal{}, err
			}
			if !condition {
				return signal{}, nil
			}
		}
		sig, err := e.evalBlock(stmt.Body, loopEnv)
		if err != nil {
			return signal{}, err
		}
		switch sig.kind {
		case "break":
			return signal{}, nil
		case "continue", "":
		default:
			return sig, nil
		}
		if sig.exited {
			return sig, nil
		}
		if stmt.Update != nil {
			if _, err := e.evalStatement(stmt.Update, loopEnv); err != nil {
				return signal{}, err
			}
		}
	}
}

func (e *Evaluator) evalTryStatement(stmt *ast.TryStatement, env *runtime.Environment) (signal, error) {
	sig, err := e.evalBlock(stmt.Body, env)
	if err != nil {
		return signal{}, err
	}
	if sig.kind == "throw" && sig.thrown != nil && !sig.thrown.IsFatal() {
		handled := false
		for _, clause := range stmt.Catches {
			if !e.catchMatches(clause, *sig.thrown) {
				continue
			}
			catchEnv := runtime.NewEnclosedEnvironment(env)
			if clause.Name != nil {
				if err := catchEnv.Define(clause.Name.Value, *sig.thrown, false); err != nil {
					return signal{}, err
				}
			}
			sig, err = e.evalBlock(clause.Body, catchEnv)
			if err != nil {
				return signal{}, err
			}
			handled = true
			break
		}
		if !handled {
			// Preserve the original throw if no catch matches.
			sig = signal{kind: "throw", thrown: sig.thrown}
		}
	}
	if stmt.Finally != nil {
		finallySig, err := e.evalBlock(stmt.Finally, env)
		if err != nil {
			return signal{}, err
		}
		if finallySig.kind != "" || finallySig.exited {
			return finallySig, nil
		}
	}
	return sig, nil
}

// evalWithStatement implements `with (expr) { ... }` and
// `with (name = expr) { ... }`. The bound value's `__enter__()`
// (if defined) supplies the binding; otherwise the binding is the
// expression itself. At any block exit - normal completion,
// exception, return, break, or continue - the runtime invokes
// `__exit__()` when present, else the class destructor
// (`~ClassName`) when present, else nothing. Errors from cleanup
// shadow a clean exit but defer to an in-flight exception.
func (e *Evaluator) evalWithStatement(stmt *ast.WithStatement, env *runtime.Environment) (signal, error) {
	resource, err := e.evalExpression(stmt.Value, env)
	if err != nil {
		return signal{}, err
	}
	bound := resource
	if instance, ok := resource.(*runtime.Instance); ok {
		if method, ok := lookupDunderEval(instance.Class, "__enter", "__enter__"); ok {
			value, err := e.applyFunctionWithThis(method, nil, instance)
			if err != nil {
				return signal{}, err
			}
			bound = value
		}
	}
	scope := runtime.NewEnclosedEnvironment(env)
	if stmt.Name != nil {
		if err := scope.Define(stmt.Name.Value, bound, false); err != nil {
			return signal{}, err
		}
	}
	sig, bodyErr := e.evalBlock(stmt.Body, scope)
	cleanupErr := e.runWithCleanup(resource)
	// If the body produced an exception (Go error or thrown
	// signal), an exception from cleanup is suppressed - matches
	// C++/Python semantics where destructor failures shouldn't
	// mask the original failure.
	if bodyErr != nil {
		return signal{}, bodyErr
	}
	if sig.kind == "throw" {
		return sig, nil
	}
	if cleanupErr != nil {
		return signal{}, cleanupErr
	}
	return sig, nil
}

// evalDelStatement implements `del x`. Looks up the binding,
// invokes the destructor (when the bound value is a class
// instance whose class declares one and hasn't already been
// destroyed), removes the entry from `e.destructibleInstances`,
// and removes the binding from the surrounding environment.
func (e *Evaluator) evalDelStatement(stmt *ast.DelStatement, env *runtime.Environment) error {
	if stmt.Target == nil {
		return fmt.Errorf("del requires an identifier")
	}
	name := stmt.Target.Value
	value, ok := env.Get(name)
	if !ok {
		return fmt.Errorf("del: unknown identifier %q", name)
	}
	if instance, ok := value.(*runtime.Instance); ok && instance != nil && !instance.Destroyed && instance.Class != nil && instance.Class.Destructor != nil {
		// Mark destroyed before the call so a destructor that
		// somehow triggers `del this` doesn't recurse.
		instance.Destroyed = true
		// Unregister from the lifetime list before invoking so the
		// program-exit sweep doesn't visit it again.
		for i, tracked := range e.destructibleInstances {
			if tracked == instance {
				e.destructibleInstances = append(e.destructibleInstances[:i], e.destructibleInstances[i+1:]...)
				break
			}
		}
		if _, err := e.applyFunctionWithThis(*instance.Class.Destructor, nil, instance); err != nil {
			return err
		}
	}
	env.Delete(name)
	return nil
}

// runWithCleanup invokes __exit__ when the resource is a class
// instance whose class defines that magic method. Otherwise it is a
// no-op: the destructor (if any) belongs to the object's lifetime,
// not to the with-block's scope, and fires later via the program-
// exit sweep or an explicit `del`.
func (e *Evaluator) runWithCleanup(resource runtime.Value) error {
	instance, ok := resource.(*runtime.Instance)
	if !ok {
		return nil
	}
	if method, ok := lookupDunderEval(instance.Class, "__exit", "__exit__"); ok {
		_, err := e.applyFunctionWithThis(method, nil, instance)
		return err
	}
	return nil
}

func lookupDunderEval(class *runtime.Class, canonical, legacy string) (runtime.Function, bool) {
	if m, ok := lookupMethod(class, canonical); ok {
		return m, true
	}
	return lookupMethod(class, legacy)
}

func lookupStaticDunderEval(class *runtime.Class, canonical, legacy string) (runtime.Function, bool) {
	if m, ok := lookupStaticMethod(class, canonical); ok {
		return m, true
	}
	return lookupStaticMethod(class, legacy)
}

func catchMatches(clause ast.CatchClause, err runtime.Error) bool {
	if clause.Type == nil {
		return true
	}
	return errorTypeMatches(err.Class, clause.Type.Name)
}

func errorTypeMatches(class string, target string) bool {
	if target == "" || target == "Error" {
		return true
	}
	for current := class; current != ""; current = errorParent(current) {
		if current == target {
			return true
		}
	}
	return false
}

func errorParent(class string) string {
	switch class {
	case "RuntimeError", "TypeError", "ValueError", "IOError", "ParseError", "MatchError", "ImmutableError", "PermissionError", "AssertionError":
		return "Error"
	default:
		return ""
	}
}

func (e *Evaluator) errorParent(class string) string {
	if parent, ok := e.errorClassParents[class]; ok {
		return parent
	}
	switch class {
	case "RuntimeError", "TypeError", "ValueError", "IOError", "ParseError", "MatchError", "ImmutableError", "PermissionError", "AssertionError":
		return "Error"
	default:
		return ""
	}
}

func (e *Evaluator) errorTypeMatches(class string, target string) bool {
	if target == "" || target == "Error" {
		// FatalError is its own tier, not an Error.
		return class != "FatalError"
	}
	for current := class; current != ""; current = e.errorParent(current) {
		if current == target {
			return true
		}
	}
	return false
}

func (e *Evaluator) catchMatches(clause ast.CatchClause, err runtime.Error) bool {
	if clause.Type == nil {
		return true
	}
	// Strip an optional module prefix - `catch (errors.HttpException e)`
	// matches the bare "HttpException" the parent chain records.
	return e.errorTypeMatches(err.Class, simpleTypeName(clause.Type.Name))
}

func (e *Evaluator) evalForInStatement(stmt *ast.ForStatement, loopEnv *runtime.Environment) (signal, error) {
	iterable, err := e.evalExpression(stmt.Iterable, loopEnv)
	if err != nil {
		return signal{}, err
	}
	names := stmt.VarNames
	if len(names) == 0 && stmt.VarName != nil {
		names = []*ast.Identifier{stmt.VarName}
	}
	if len(names) == 0 {
		return signal{}, fmt.Errorf("for-in loop has no loop variable")
	}
	for _, name := range names {
		if err := loopEnv.Define(name.Value, runtime.Null{}, false); err != nil {
			return signal{}, err
		}
	}
	if r, ok := iterable.(runtime.Range); ok {
		current := new(big.Int).Set(r.Start)
		step := new(big.Int).Set(r.Step)
		for {
			cmp := current.Cmp(r.End)
			if step.Sign() > 0 {
				if r.Exclusive && cmp >= 0 {
					break
				}
				if !r.Exclusive && cmp > 0 {
					break
				}
			} else {
				if r.Exclusive && cmp <= 0 {
					break
				}
				if !r.Exclusive && cmp < 0 {
					break
				}
			}
			sig, err := e.evalForInIteration(stmt, loopEnv, names, runtime.Int{Value: new(big.Int).Set(current)})
			if err != nil {
				return signal{}, err
			}
			switch sig.kind {
			case "break":
				return signal{}, nil
			case "continue", "":
			default:
				return sig, nil
			}
			if sig.exited {
				return sig, nil
			}
			current.Add(current, step)
		}
		return signal{}, nil
	}
	if generator, ok := iterable.(*runtime.Generator); ok {
		defer generator.Close()
		for {
			value, ok, err := generator.Next()
			if err != nil {
				return signal{}, err
			}
			if !ok {
				break
			}
			sig, err := e.evalForInIteration(stmt, loopEnv, names, value)
			if err != nil {
				return signal{}, err
			}
			switch sig.kind {
			case "break":
				return signal{}, nil
			case "continue", "":
			default:
				return sig, nil
			}
			if sig.exited {
				return sig, nil
			}
		}
		return signal{}, nil
	}
	if instance, ok := iterable.(*runtime.Instance); ok {
		iter, passthrough, iterableOk, err := e.resolveUserIterator(instance)
		if err != nil {
			return signal{}, err
		}
		if iterableOk {
			if passthrough != nil {
				iterable = passthrough
				goto dispatchPassthrough
			}
			for {
				if doneFn, ok := lookupMethod(iter.Class, "__done"); ok {
					doneResult, err := e.applyFunctionWithThis(doneFn, nil, iter)
					if err != nil {
						return signal{}, err
					}
					doneBool, ok := doneResult.(runtime.Bool)
					if !ok {
						return signal{}, fmt.Errorf("%s.__done must return bool, got %s", iter.Class.Name, doneResult.TypeName())
					}
					if doneBool.Value {
						break
					}
				}
				nextFn, ok := lookupMethod(iter.Class, "__next")
				if !ok {
					return signal{}, fmt.Errorf("%s is not an iterator: define __next()", iter.Class.Name)
				}
				value, err := e.applyFunctionWithThis(nextFn, nil, iter)
				if err != nil {
					return signal{}, err
				}
				sig, err := e.evalForInIteration(stmt, loopEnv, names, value)
				if err != nil {
					return signal{}, err
				}
				switch sig.kind {
				case "break":
					return signal{}, nil
				case "continue", "":
				default:
					return sig, nil
				}
				if sig.exited {
					return sig, nil
				}
			}
			return signal{}, nil
		}
	}
dispatchPassthrough:
	if generator, ok := iterable.(*runtime.Generator); ok {
		defer generator.Close()
		for {
			value, ok, err := generator.Next()
			if err != nil {
				return signal{}, err
			}
			if !ok {
				break
			}
			sig, err := e.evalForInIteration(stmt, loopEnv, names, value)
			if err != nil {
				return signal{}, err
			}
			switch sig.kind {
			case "break":
				return signal{}, nil
			case "continue", "":
			default:
				return sig, nil
			}
			if sig.exited {
				return sig, nil
			}
		}
		return signal{}, nil
	}
	values, err := e.iterableValues(iterable)
	if err != nil {
		return signal{}, err
	}
	for _, value := range values {
		sig, err := e.evalForInIteration(stmt, loopEnv, names, value)
		if err != nil {
			return signal{}, err
		}
		switch sig.kind {
		case "break":
			return signal{}, nil
		case "continue", "":
		default:
			return sig, nil
		}
		if sig.exited {
			return sig, nil
		}
	}
	return signal{}, nil
}

func (e *Evaluator) evalForInIteration(stmt *ast.ForStatement, loopEnv *runtime.Environment, names []*ast.Identifier, value runtime.Value) (signal, error) {
	if len(names) == 1 {
		if err := loopEnv.Assign(names[0].Value, value); err != nil {
			return signal{}, err
		}
	} else {
		list, ok := value.(*runtime.List)
		if !ok || len(list.Elements) != len(names) {
			return signal{}, fmt.Errorf("cannot destructure %s into %d loop variables", value.TypeName(), len(names))
		}
		for i, name := range names {
			if err := loopEnv.Assign(name.Value, list.Elements[i]); err != nil {
				return signal{}, err
			}
		}
	}
	return e.evalBlock(stmt.Body, loopEnv)
}

func (e *Evaluator) walkComprehensionClauses(clauses []ast.ComprehensionClause, idx int, env *runtime.Environment, body func(*runtime.Environment) error) error {
	if idx >= len(clauses) {
		return body(env)
	}
	switch c := clauses[idx].(type) {
	case *ast.ComprehensionIf:
		v, err := e.evalExpression(c.Filter, env)
		if err != nil {
			return err
		}
		if !isTruthy(v) {
			return nil
		}
		return e.walkComprehensionClauses(clauses, idx+1, env, body)
	case *ast.ComprehensionFor:
		return e.iterateComprehensionFor(c, env, func(itEnv *runtime.Environment) error {
			return e.walkComprehensionClauses(clauses, idx+1, itEnv, body)
		})
	}
	return fmt.Errorf("unknown comprehension clause %T", clauses[idx])
}

func (e *Evaluator) iterateComprehensionFor(c *ast.ComprehensionFor, env *runtime.Environment, body func(*runtime.Environment) error) error {
	iterable, err := e.evalExpression(c.Iterable, env)
	if err != nil {
		return err
	}
	names := c.VarNames
	if len(names) == 0 && c.VarName != nil {
		names = []*ast.Identifier{c.VarName}
	}
	if len(names) == 0 {
		return fmt.Errorf("comprehension `for` has no loop variable")
	}
	step := func(value runtime.Value) error {
		itEnv := runtime.NewEnclosedEnvironment(env)
		if len(names) == 1 {
			if err := itEnv.Define(names[0].Value, value, false); err != nil {
				return err
			}
		} else {
			list, ok := value.(*runtime.List)
			if !ok || len(list.Elements) != len(names) {
				return fmt.Errorf("cannot destructure %s into %d comprehension variables", value.TypeName(), len(names))
			}
			for i, name := range names {
				if err := itEnv.Define(name.Value, list.Elements[i], false); err != nil {
					return err
				}
			}
		}
		return body(itEnv)
	}
	switch it := iterable.(type) {
	case *runtime.List:
		for _, el := range it.Elements {
			if err := step(el); err != nil {
				return err
			}
		}
		return nil
	case runtime.Set:
		ordered := orderedSetValues(it)
		for _, v := range ordered {
			if err := step(v); err != nil {
				return err
			}
		}
		return nil
	case runtime.Range:
		current := new(big.Int).Set(it.Start)
		step := new(big.Int).Set(it.Step)
		fn := func() error {
			for {
				cmp := current.Cmp(it.End)
				if step.Sign() > 0 {
					if it.Exclusive && cmp >= 0 {
						return nil
					}
					if !it.Exclusive && cmp > 0 {
						return nil
					}
				} else {
					if it.Exclusive && cmp <= 0 {
						return nil
					}
					if !it.Exclusive && cmp < 0 {
						return nil
					}
				}
				if err := body(envWithSingleBinding(env, names[0].Value, runtime.Int{Value: new(big.Int).Set(current)})); err != nil {
					return err
				}
				current.Add(current, step)
			}
		}
		return fn()
	case *runtime.Generator:
		defer it.Close()
		for {
			v, ok, err := it.Next()
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if err := step(v); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("comprehension cannot iterate over %s", iterable.TypeName())
}

func envWithSingleBinding(parent *runtime.Environment, name string, value runtime.Value) *runtime.Environment {
	itEnv := runtime.NewEnclosedEnvironment(parent)
	_ = itEnv.Define(name, value, false)
	return itEnv
}

func (e *Evaluator) evalDestructuringStatement(stmt *ast.DestructuringStatement, env *runtime.Environment) (signal, error) {
	val, err := e.evalExpression(stmt.Value, env)
	if err != nil {
		return signal{}, err
	}
	if stmt.IsList {
		list, ok := val.(*runtime.List)
		if !ok {
			return signal{}, fmt.Errorf("cannot list-destructure %s", val.TypeName())
		}
		if len(list.Elements) < len(stmt.Names) {
			return signal{}, fmt.Errorf("list has %d elements, destructuring expects %d", len(list.Elements), len(stmt.Names))
		}
		for i, name := range stmt.Names {
			if err := bindDestructuredName(env, name.Value, list.Elements[i], stmt.Define); err != nil {
				return signal{}, err
			}
		}
	} else {
		dict, ok := val.(runtime.Dict)
		if !ok {
			return signal{}, fmt.Errorf("cannot dict-destructure %s", val.TypeName())
		}
		for i, name := range stmt.Names {
			key := name.Value
			if i < len(stmt.Keys) && stmt.Keys[i] != "" {
				key = stmt.Keys[i]
			}
			v, ok := dictField(dict, key)
			if !ok {
				v = runtime.Null{}
			}
			if err := bindDestructuredName(env, name.Value, v, stmt.Define); err != nil {
				return signal{}, err
			}
		}
	}
	return signal{}, nil
}

func bindDestructuredName(env *runtime.Environment, name string, value runtime.Value, define bool) error {
	if define {
		return env.Define(name, value, false)
	}
	return env.Assign(name, value)
}

// resolveUserIterator returns the iterator backing a `for (x in obj)`
// loop where obj is a user-defined class instance. Calls obj.__iter()
// when defined and returns:
//   - (*runtime.Instance, true, nil) when the result is itself an
//     iterator instance (has __next or is `this` for self-iterating
//     classes).
//   - (any other runtime.Value via the "passthrough" path, true, nil)
//     by setting the value into `passthrough` so the caller falls back
//     to the standard iteration dispatcher on List / Generator / etc.
//   - (nil, false, nil) when the instance implements neither protocol
//     side (caller surfaces the existing "not iterable" error).
func (e *Evaluator) resolveUserIterator(instance *runtime.Instance) (*runtime.Instance, runtime.Value, bool, error) {
	if instance == nil || instance.Class == nil {
		return nil, nil, false, nil
	}
	if iterFn, ok := lookupMethod(instance.Class, "__iter"); ok {
		result, err := e.applyFunctionWithThis(iterFn, nil, instance)
		if err != nil {
			return nil, nil, true, err
		}
		if inst, ok := result.(*runtime.Instance); ok {
			if inst == instance {
				if _, hasNext := lookupMethod(instance.Class, "__next"); hasNext {
					return instance, nil, true, nil
				}
			}
			if _, hasNext := lookupMethod(inst.Class, "__next"); hasNext {
				return inst, nil, true, nil
			}
			return nil, inst, true, nil
		}
		return nil, result, true, nil
	}
	if _, ok := lookupMethod(instance.Class, "__next"); ok {
		return instance, nil, true, nil
	}
	return nil, nil, false, nil
}

func (e *Evaluator) iterableValues(value runtime.Value) ([]runtime.Value, error) {
	switch value := value.(type) {
	case *runtime.List:
		return value.Elements, nil
	case *runtime.Generator:
		values := []runtime.Value{}
		for {
			next, ok, err := value.Next()
			if err != nil {
				return nil, err
			}
			if !ok {
				return values, nil
			}
			values = append(values, next)
		}
	case runtime.Dict:
		ordered := value.OrderedKeys()
		values := make([]runtime.Value, 0, len(ordered))
		for _, k := range ordered {
			entry := value.Entries[k]
			values = append(values, &runtime.List{Elements: []runtime.Value{entry.Key, entry.Value}})
		}
		return values, nil
	default:
		return nil, fmt.Errorf("%s is not iterable", value.TypeName())
	}
}

func (e *Evaluator) evalBoolCondition(expr ast.Expression, env *runtime.Environment) (bool, error) {
	value, err := e.evalExpression(expr, env)
	if err != nil {
		return false, err
	}
	boolValue, ok := value.(runtime.Bool)
	if !ok {
		return false, fmt.Errorf("condition must be bool, got %s", value.TypeName())
	}
	return boolValue.Value, nil
}

// applyCallableValue dispatches a CallExpression whose callee has
// already been evaluated to a runtime value. Used both for the normal
// "callee is an expression" path and for the parenthesized-selector
// form `(obj.fn)(args)` that must invoke obj.fn's value rather than
// dispatch as a method call.
func (e *Evaluator) applyCallableValue(callee runtime.Value, call *ast.CallExpression, env *runtime.Environment, expected *ast.TypeRef) (runtime.Value, error) {
	result, err := e.applyCallableValueRaw(callee, call, env, expected)
	if err == nil {
		e.stampDecoratedClassResult(call.Callee, result)
	}
	return result, err
}

func (e *Evaluator) applyCallableValueRaw(callee runtime.Value, call *ast.CallExpression, env *runtime.Environment, expected *ast.TypeRef) (runtime.Value, error) {
	if fn, ok := callee.(runtime.Function); ok {
		args, err := e.evalFunctionCallArguments(fn, call, env)
		if err != nil {
			return nil, err
		}
		if fn.ForwardThis {
			if value, ok := env.Get("this"); ok {
				if instance, ok := value.(*runtime.Instance); ok {
					return e.applyFunctionWithThis(fn, args, instance)
				}
			}
		}
		return e.applyFunctionWithTypeArgs(fn, args, call.TypeArguments)
	}
	if overloaded, ok := callee.(runtime.OverloadedFunction); ok {
		return e.applyOverloadedFunction(overloaded.Name, overloaded.Overloads, call, env, nil, expected)
	}
	if class, ok := callee.(*runtime.Class); ok {
		return e.instantiateClassFromCall(class, call, env, expected)
	}
	if instance, ok := callee.(*runtime.Instance); ok {
		method, ok := lookupMethod(instance.Class, "__invoke")
		if !ok {
			return nil, fmt.Errorf("%s is not callable", call.Callee.String())
		}
		args, err := e.evalFunctionCallArguments(method, call, env)
		if err != nil {
			return nil, err
		}
		return e.applyFunctionWithThis(method, args, instance)
	}
	return nil, fmt.Errorf("%s is not callable", call.Callee.String())
}

func (e *Evaluator) stampDecoratedClassResult(callee ast.Expression, result runtime.Value) {
	ident, ok := callee.(*ast.Identifier)
	if !ok {
		return
	}
	className, ok := e.decoratedClassIdents[ident.Value]
	if !ok {
		return
	}
	instance, ok := result.(*runtime.Instance)
	if !ok || instance == nil || instance.Class == nil {
		return
	}
	if instance.Class.Name == className {
		return
	}
	instance.ExtraTypeNames = append(instance.ExtraTypeNames, className)
}

func (e *Evaluator) evalCallWithExpectedType(call *ast.CallExpression, env *runtime.Environment, expected *ast.TypeRef) (runtime.Value, error) {
	if ident, ok := call.Callee.(*ast.Identifier); ok && ident.Value == "parent" {
		this, err := currentInstance(env)
		if err != nil {
			return nil, err
		}
		// Resolve the lexical class (the class whose method body is currently
		// executing) rather than the runtime class of `this`. Falling back to
		// `this.Class` is only correct when the method body is directly that
		// of the runtime class - otherwise parent() inside an inherited
		// constructor would re-target the same class it lives in.
		lexicalClass := this.Class
		if len(e.classStack) > 0 {
			lexicalClass = e.classStack[len(e.classStack)-1]
		}
		parentClass := lexicalClass.Parent
		if parentClass == nil || len(parentClass.Constructors) == 0 {
			// For builtin error parents, capture the message from the first string arg.
			if isErrorDerived(this.Class) && len(call.Arguments) > 0 {
				args, err := e.evalCallArguments(call, env)
				if err != nil {
					return nil, err
				}
				for _, arg := range args {
					if s, ok := arg.(runtime.String); ok {
						this.Fields["__parentMsg"] = s
						break
					}
				}
			}
			return runtime.Null{}, nil
		}
		_, err = e.applyOverloadedFunction(parentClass.Name, parentClass.Constructors, call, env, this, nil)
		if err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	}
	if ident, ok := call.Callee.(*ast.Identifier); ok && ident.Value == "typeof" {
		args, err := e.evalCallArguments(call, env)
		if err != nil {
			return nil, err
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("typeof expects exactly one argument")
		}
		switch evalTypeofArg := args[0].(type) {
		case *runtime.Class:
			return runtime.Type{Name: evalTypeofArg.Name}, nil
		case runtime.BytecodeClass:
			return runtime.Type{Name: evalTypeofArg.Name}, nil
		default:
			return runtime.Type{Name: args[0].TypeName()}, nil
		}
	}
	if ident, ok := call.Callee.(*ast.Identifier); ok && strings.EqualFold(ident.Value, "dump") {
		return e.evalDumpCall(call, env)
	}
	if ident, ok := call.Callee.(*ast.Identifier); ok && strings.EqualFold(ident.Value, "dir") {
		return e.evalDirCall(call, env)
	}
	if ident, ok := call.Callee.(*ast.Identifier); ok && ident.Value == "range" {
		return e.evalRangeBuiltin(call, env)
	}
	if ident, ok := call.Callee.(*ast.Identifier); ok && isBuiltinErrorClass(ident.Value) {
		args, err := e.evalCallArguments(call, env)
		if err != nil {
			return nil, err
		}
		return newErrorValue(ident.Value, args)
	}
	if ident, ok := call.Callee.(*ast.Identifier); ok && ident.Value == "assert" {
		return e.evalAssertCall(call, env)
	}
	// A parenthesized selector like `(this.fn)(args)` is a
	// "call the value" expression, not a method call. Bypass the
	// module / method dispatch and evaluate the selector as a value.
	if selector, ok := call.Callee.(*ast.SelectorExpression); ok && selector.Parenthesized {
		callee, err := e.evalExpression(call.Callee, env)
		if err != nil {
			return nil, err
		}
		return e.applyCallableValue(callee, call, env, expected)
	}
	module, name, ok := selectorName(call.Callee)
	/* Local shadowing wins over module-call dispatch: if the
	 * receiver of an `X.Y(...)` call resolves in the current
	 * scope to a non-module value (a list, a class instance, a
	 * closure, ...), treat it as a method call on that value
	 * rather than as a module-export lookup. Important because
	 * the evaluator's `imports` table is process-wide - a sibling
	 * module's `import errors as errors` would otherwise cause
	 * `let errors = []` in our function to dispatch to the
	 * unrelated module's namespace. */
	if ok {
		if value, found := env.Get(module); found {
			if _, isModule := value.(*runtime.Module); !isModule {
				if selector, ok := call.Callee.(*ast.SelectorExpression); ok {
					if v, handled, err := e.evalParentMethodCall(selector, call, env); handled {
						return v, err
					}
					receiver, err := e.evalExpression(selector.Object, env)
					if err != nil {
						return nil, err
					}
					if selector.Optional {
						if _, isNull := receiver.(runtime.Null); isNull {
							return runtime.Null{}, nil
						}
					}
					return e.evalMethodCallExpression(receiver, selector.Name.Value, call, env)
				}
			}
		}
	}
	if !ok {
		if selector, ok := call.Callee.(*ast.SelectorExpression); ok && !selector.Parenthesized {
			if value, handled, err := e.evalParentMethodCall(selector, call, env); handled {
				return value, err
			}
			receiver, err := e.evalExpression(selector.Object, env)
			if err != nil {
				return nil, err
			}
			if selector.Optional {
				if _, isNull := receiver.(runtime.Null); isNull {
					return runtime.Null{}, nil
				}
			}
			return e.evalMethodCallExpression(receiver, selector.Name.Value, call, env)
		}
		callee, err := e.evalExpression(call.Callee, env)
		if err != nil {
			return nil, err
		}
		return e.applyCallableValue(callee, call, env, expected)
	}
	if strings.EqualFold(module, "reflect") && (name == "function" || name == "class" || name == "module") {
		return e.evalReflectLookupCall(call, env, name)
	}
	if strings.EqualFold(module, "reflect") && name == "classes" {
		return e.evalReflectClassesCall(call, env)
	}
	if !e.imports[module] {
		if selector, ok := call.Callee.(*ast.SelectorExpression); ok {
			if value, handled, err := e.evalParentMethodCall(selector, call, env); handled {
				return value, err
			}
			receiver, err := e.evalExpression(selector.Object, env)
			if err != nil {
				return nil, err
			}
			if selector.Optional {
				if _, isNull := receiver.(runtime.Null); isNull {
					return runtime.Null{}, nil
				}
			}
			return e.evalMethodCallExpression(receiver, selector.Name.Value, call, env)
		}
		return nil, fmt.Errorf("module %q has not been imported", module)
	}

	/* Prefer the env-local Module binding's `Canonical` over the
	 * shared `e.importNames` map: different files may legitimately
	 * alias the same identifier to different canonical modules
	 * (e.g. a user file `import web.websocket as websocket;` while
	 * stdlib `import websocket;` keeps the native), and the shared
	 * map only holds the last write. The env-local binding is what
	 * the current scope sees, so it's the authoritative source. */
	canonical, hasImportName := e.importNames[module]
	if envValue, ok := env.Get(module); ok {
		if mod, ok := envValue.(*runtime.Module); ok && mod.Canonical != "" {
			canonical = mod.Canonical
			hasImportName = true
		}
	}
	functions, ok := map[string]builtinFunc(nil), false
	if hasImportName {
		functions, ok = e.builtins[canonical]
	} else {
		functions, ok = e.builtins[module]
	}
	if !ok {
		return e.callModuleExport(module, name, call, env, expected)
	}
	function, ok := functions[name]
	if !ok {
		if e.moduleExportExists(module, name, env) {
			return e.callModuleExport(module, name, call, env, expected)
		}
		return nil, fmt.Errorf("unknown function %s.%s", module, name)
	}
	args, err := e.evalCallArguments(call, env)
	if err != nil {
		return nil, err
	}
	return function(call, args)
}

func (e *Evaluator) moduleExportExists(module, name string, env *runtime.Environment) bool {
	moduleValue, ok := env.Get(module)
	if !ok {
		return false
	}
	userModule, ok := moduleValue.(*runtime.Module)
	if !ok {
		return false
	}
	_, ok = userModule.Exports[name]
	return ok
}

func (e *Evaluator) callModuleExport(module, name string, call *ast.CallExpression, env *runtime.Environment, expected *ast.TypeRef) (runtime.Value, error) {
	moduleValue, valueOK := env.Get(module)
	if !valueOK {
		return nil, fmt.Errorf("unknown module %s", module)
	}
	userModule, moduleOK := moduleValue.(*runtime.Module)
	if !moduleOK {
		return nil, fmt.Errorf("%s is not a module", module)
	}
	value, exportOK := userModule.Exports[name]
	if !exportOK {
		return nil, fmt.Errorf("module %s has no export %s", module, name)
	}
	if fn, ok := value.(runtime.Function); ok {
		args, err := e.evalFunctionCallArguments(fn, call, env)
		if err != nil {
			return nil, err
		}
		return e.applyFunction(fn, args)
	}
	if overloaded, ok := value.(runtime.OverloadedFunction); ok {
		return e.applyOverloadedFunction(module+"."+name, overloaded.Overloads, call, env, nil, expected)
	}
	if class, ok := value.(*runtime.Class); ok {
		return e.instantiateClassFromCall(class, call, env, expected)
	}
	return nil, fmt.Errorf("%s.%s is not callable", module, name)
}

func (e *Evaluator) evalReflectClassesCall(call *ast.CallExpression, env *runtime.Environment) (runtime.Value, error) {
	args, err := e.evalCallArguments(call, env)
	if err != nil {
		return nil, err
	}
	if len(args) != 0 {
		return nil, fmt.Errorf("reflect.classes takes no arguments")
	}
	names := make([]string, 0, len(e.globalClasses))
	for n := range e.globalClasses {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]runtime.Value, 0, len(names))
	for _, n := range names {
		out = append(out, e.globalClasses[n])
	}
	return &runtime.List{Elements: out}, nil
}

func (e *Evaluator) evalReflectLookupCall(call *ast.CallExpression, env *runtime.Environment, name string) (runtime.Value, error) {
	args, err := e.evalCallArguments(call, env)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("reflect.%s expects exactly one argument", name)
	}
	// `reflect.class` / `reflect.function` / `reflect.module` accept either
	// a name string (legacy) or a value of the appropriate kind. Passing
	// the value itself lets framework code work uniformly with the VM
	// backend, which has always accepted values directly.
	if name == "class" {
		switch v := args[0].(type) {
		case *runtime.Class:
			return v, nil
		case *runtime.Instance:
			if v != nil && v.Class != nil {
				return v.Class, nil
			}
			return runtime.Null{}, nil
		}
	}
	if name == "function" {
		switch v := args[0].(type) {
		case runtime.Function, runtime.OverloadedFunction:
			return v, nil
		}
	}
	if name == "module" {
		if mod, ok := args[0].(*runtime.Module); ok {
			return mod, nil
		}
	}
	targetName, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("reflect.%s argument must be a %s value or name string", name, name)
	}
	value, ok, err := e.reflectLookupValue(targetName.Value, env)
	if err != nil {
		return nil, err
	}
	if !ok && name == "class" {
		// Fall back to the cross-module class registry so framework
		// code can resolve a class by name even when the local env
		// doesn't have it (the user's classes live in their own
		// module's env, but a framework helper imported into the
		// user's program still needs to reflect on them).
		if class, found := e.globalClasses[targetName.Value]; found {
			return class, nil
		}
	}
	if !ok {
		return runtime.Null{}, nil
	}
	switch name {
	case "function":
		switch value := value.(type) {
		case runtime.Function, runtime.OverloadedFunction:
			return value, nil
		default:
			return nil, fmt.Errorf("reflect.function %q is %s, not function", targetName.Value, value.TypeName())
		}
	case "class":
		class, ok := value.(*runtime.Class)
		if !ok {
			return nil, fmt.Errorf("reflect.class %q is %s, not class", targetName.Value, value.TypeName())
		}
		return class, nil
	case "module":
		module, ok := value.(*runtime.Module)
		if !ok {
			return nil, fmt.Errorf("reflect.module %q is %s, not module", targetName.Value, value.TypeName())
		}
		return module, nil
	default:
		return nil, fmt.Errorf("unsupported reflect lookup %s", name)
	}
}

func (e *Evaluator) reflectLookupValue(name string, env *runtime.Environment) (runtime.Value, bool, error) {
	if moduleName, exportName, ok := strings.Cut(name, "."); ok {
		moduleValue, valueOK := env.Get(moduleName)
		if !valueOK {
			return nil, false, nil
		}
		module, ok := moduleValue.(*runtime.Module)
		if !ok {
			return nil, false, fmt.Errorf("reflect lookup %q: %s is not a module", name, moduleName)
		}
		value, ok := module.Exports[exportName]
		return value, ok, nil
	}
	value, ok := env.Get(name)
	return value, ok, nil
}

func (e *Evaluator) evalDirCall(call *ast.CallExpression, env *runtime.Environment) (runtime.Value, error) {
	if len(call.Arguments) > 1 {
		return nil, fmt.Errorf("dir expects zero or one argument")
	}
	if len(call.Arguments) == 0 {
		return stringList(env.Names()), nil
	}
	if call.Arguments[0].Name != nil {
		return nil, fmt.Errorf("dir does not accept named arguments")
	}
	if ident, ok := call.Arguments[0].Value.(*ast.Identifier); ok {
		if names, ok := e.dirImportedModule(ident.Value); ok {
			return stringList(names), nil
		}
	}
	value, err := e.evalExpression(call.Arguments[0].Value, env)
	if err != nil {
		return nil, err
	}
	return stringList(dirValue(value)), nil
}

func (e *Evaluator) evalDumpCall(call *ast.CallExpression, env *runtime.Environment) (runtime.Value, error) {
	if len(call.Arguments) != 1 {
		return nil, fmt.Errorf("dump expects exactly one argument")
	}
	if call.Arguments[0].Name != nil {
		return nil, fmt.Errorf("dump does not accept named arguments")
	}
	value, err := e.evalExpression(call.Arguments[0].Value, env)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: native.DumpValue(value)}, nil
}

func (e *Evaluator) dirImportedModule(alias string) ([]string, bool) {
	canonical, ok := e.importNames[alias]
	if !ok {
		return nil, false
	}
	functions, ok := e.builtins[canonical]
	if !ok {
		functions, ok = e.builtins[alias]
	}
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	for _, name := range e.builtinModuleTypeExportNames(canonical) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

func (e *Evaluator) builtinModuleTypeExportNames(canonical string) []string {
	switch canonical {
	case "http":
		return []string{"Cookie", "Headers", "Request", "Response"}
	case "test":
		return []string{"Test"}
	case "json":
		return []string{"JsonStreamInterface"}
	case "xml":
		return []string{"XmlStreamInterface"}
	case "yaml":
		return []string{"YamlStreamInterface"}
	case "csv":
		return []string{"CsvStreamInterface"}
	case "log":
		return []string{"LogInterface"}
	default:
		return nil
	}
}

func dirValue(value runtime.Value) []string {
	names := []string{}
	switch value := value.(type) {
	case *runtime.Module:
		for name := range value.Exports {
			names = append(names, name)
		}
	case *runtime.Class:
		seen := map[string]bool{}
		for class := value; class != nil; class = class.Parent {
			for _, field := range class.Fields {
				seen[field.Name] = true
			}
			for name := range class.Methods {
				seen[name] = true
			}
			for name := range class.StaticMethods {
				seen[name] = true
			}
			for name := range class.StaticValues {
				seen[name] = true
			}
		}
		for name := range seen {
			names = append(names, name)
		}
	case *runtime.Instance:
		seen := map[string]bool{}
		for name := range value.Fields {
			seen[name] = true
		}
		for class := value.Class; class != nil; class = class.Parent {
			for _, field := range class.Fields {
				seen[field.Name] = true
			}
			for name := range class.Methods {
				seen[name] = true
			}
		}
		for name := range seen {
			names = append(names, name)
		}
	case runtime.Dict:
		names = primitiveMethodNamesFor("dict")
	case runtime.Set:
		names = primitiveMethodNamesFor("set")
	case *runtime.List:
		names = primitiveMethodNamesFor("list")
	case runtime.String:
		names = primitiveMethodNamesFor("string")
	case runtime.Bytes:
		names = primitiveMethodNamesFor("bytes")
	case runtime.Range:
		names = primitiveMethodNamesFor("range")
	case runtime.SmallInt, runtime.Int:
		names = primitiveMethodNamesFor("int")
	case runtime.Decimal:
		names = primitiveMethodNamesFor("decimal")
	case runtime.Float:
		names = primitiveMethodNamesFor("float")
	case runtime.Bool:
		names = primitiveMethodNamesFor("bool")
	case runtime.NativeObject:
		names = nativeObjectMethods(value.Kind)
	case runtime.Function, runtime.OverloadedFunction:
		names = []string{"call"}
	default:
		names = []string{}
	}
	sort.Strings(names)
	return names
}

func nativeObjectMethods(kind string) []string {
	switch kind {
	case "IOBuffer":
		return []string{"close", "length", "reset", "toString", "write", "writeln"}
	case "IOStream":
		return []string{"close", "read", "readAll", "readBytes", "toString", "write", "writeBytes", "writeln"}
	case "IOCapture":
		return []string{"bytes", "close", "read", "readAll", "readBytes", "reset", "toString", "write", "writeBytes", "writeln"}
	case "JsonReader", "XmlReader", "CsvReader", "YamlReader":
		return []string{"close", "next"}
	default:
		return nil
	}
}

func stringList(names []string) *runtime.List {
	elements := make([]runtime.Value, 0, len(names))
	for _, name := range names {
		elements = append(elements, runtime.String{Value: name})
	}
	return &runtime.List{Elements: elements}
}

func (e *Evaluator) evalSelectorExpression(expr *ast.SelectorExpression, env *runtime.Environment) (runtime.Value, error) {
	if expr.Name.Value == "type" && !expr.Optional {
		obj, err := e.evalExpression(expr.Object, env)
		if err != nil {
			return nil, err
		}
		switch v := obj.(type) {
		case runtime.Type:
			return v, nil
		case *runtime.Class:
			return runtime.Type{Name: v.Name}, nil
		case runtime.BytecodeClass:
			return runtime.Type{Name: v.Name}, nil
		default:
			return runtime.Type{Name: obj.TypeName()}, nil
		}
	}
	object, err := e.evalExpression(expr.Object, env)
	if err != nil {
		return nil, err
	}
	if expr.Optional {
		if _, isNull := object.(runtime.Null); isNull {
			return runtime.Null{}, nil
		}
	}
	if instance, ok := object.(*runtime.Instance); ok {
		if value, ok := instance.GetField(expr.Name.Value); ok {
			return value, nil
		}
		if method, ok := lookupMethod(instance.Class, expr.Name.Value); ok {
			bound := method
			bound.Env = bindThis(method.Env, instance)
			return bound, nil
		}
		if method, ok := lookupMethod(instance.Class, "__get"); ok {
			return e.applyFunctionWithThis(method, []runtime.Value{runtime.String{Value: expr.Name.Value}}, instance)
		}
		return nil, fmt.Errorf("unknown field %s.%s", instance.Class.Name, expr.Name.Value)
	}
	if class, ok := object.(*runtime.Class); ok {
		if value, ok := lookupStaticValue(class, expr.Name.Value); ok {
			return value, nil
		}
		if method, ok := lookupStaticMethod(class, expr.Name.Value); ok {
			return method, nil
		}
		if method, ok := lookupStaticMethod(class, "__getStatic"); ok {
			return e.applyFunction(method, []runtime.Value{runtime.String{Value: expr.Name.Value}})
		}
		return nil, fmt.Errorf("unknown static member %s.%s", class.Name, expr.Name.Value)
	}
	if module, ok := object.(*runtime.Module); ok {
		if value, ok := module.Exports[expr.Name.Value]; ok {
			return value, nil
		}
		// A native module's functions are not in Exports; expose a pure one as
		// a first-class callable value (math.abs without calling it), matching
		// the bytecode VM's OpNativeValue.
		canonical := module.Canonical
		if canonical == "" {
			canonical = module.Name
		}
		if native.IsPureBuiltin(canonical, expr.Name.Value) {
			return e.wrapBuiltinAsFunction(canonical, expr.Name.Value, e.registryBuiltin(canonical, expr.Name.Value)), nil
		}
		return nil, fmt.Errorf("module %s has no export %s", module.Name, expr.Name.Value)
	}
	if typeVal, ok := object.(runtime.Type); ok {
		// Builtin type statics as first-class values (no import):
		// string.compare, bytes.fromString, ...
		if v, ok := e.nativeBuiltinValue(typeVal.Name, expr.Name.Value); ok {
			return v, nil
		}
		return nil, fmt.Errorf("%s has no static member %s", typeVal.Name, expr.Name.Value)
	}
	if errValue, ok := object.(runtime.Error); ok {
		switch expr.Name.Value {
		case "class", "name":
			return runtime.String{Value: errValue.Class}, nil
		case "message":
			return runtime.String{Value: errValue.Message}, nil
		default:
			if errValue.Fields != nil {
				if v, ok := errValue.Fields[expr.Name.Value]; ok {
					return v, nil
				}
			}
			return nil, fmt.Errorf("%s has no field %s", errValue.Class, expr.Name.Value)
		}
	}
	if task, ok := object.(*runtime.Task); ok {
		switch expr.Name.Value {
		case "done":
			return runtime.Bool{Value: task.Done()}, nil
		case "cancelled":
			return runtime.Bool{Value: task.Cancelled()}, nil
		}
		return nil, fmt.Errorf("Task has no field %s", expr.Name.Value)
	}
	if enum, ok := object.(*runtime.EnumDef); ok {
		return enumVariantValue(enum, expr.Name.Value)
	}
	if ev, ok := object.(runtime.EnumVariant); ok {
		return enumVariantField(ev, expr.Name.Value)
	}
	if r, ok := object.(runtime.Range); ok {
		switch expr.Name.Value {
		case "start":
			return runtime.Int{Value: new(big.Int).Set(r.Start)}, nil
		case "end":
			return runtime.Int{Value: new(big.Int).Set(r.End)}, nil
		case "step":
			return runtime.Int{Value: new(big.Int).Set(r.Step)}, nil
		case "length":
			return runtime.Int{Value: r.Length()}, nil
		}
		return nil, fmt.Errorf("range has no field %s", expr.Name.Value)
	}
	if expr.Name.Value == "length" {
		switch v := object.(type) {
		case runtime.String:
			return runtime.SmallInt{Value: int64(len([]rune(v.Value)))}, nil
		case runtime.Bytes:
			return runtime.SmallInt{Value: int64(len(v.Value))}, nil
		case *runtime.List:
			return runtime.SmallInt{Value: int64(len(v.Elements))}, nil
		case runtime.Dict:
			return runtime.SmallInt{Value: int64(len(v.Entries))}, nil
		case runtime.Set:
			return runtime.SmallInt{Value: int64(len(v.Elements))}, nil
		}
	}
	return nil, fmt.Errorf("unsupported selector expression %s", expr.String())
}

func (e *Evaluator) builtinModules() map[string]map[string]builtinFunc {
	return map[string]map[string]builtinFunc{
		"io": {
			"print": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				text, err := singlePrintableValue(call, args)
				if err != nil {
					return nil, err
				}
				_, err = fmt.Fprint(e.stdout, text)
				return runtime.Null{}, err
			},
			"println": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				text, err := singlePrintableValue(call, args)
				if err != nil {
					return nil, err
				}
				_, err = fmt.Fprintln(e.stdout, text)
				return runtime.Null{}, err
			},
			"readText":       ioReadText,
			"writeText":      ioWriteText,
			"appendText":     ioAppendText,
			"readBytes":      e.ioReadBytes,
			"writeBytes":     e.ioWriteBytes,
			"appendBytes":    ioAppendBytes,
			"exists":         ioExists,
			"tempFile":       ioTempFile,
			"tempDir":        ioTempDir,
			"open":           e.ioOpen,
			"memory":         e.ioMemory,
			"stdin":          e.ioStdin,
			"stdout":         e.ioStdout,
			"stderr":         e.ioStderr,
			"read":           e.ioRead,
			"readAll":        e.ioReadAll,
			"write":          e.ioWrite,
			"writeln":        e.ioWriteln,
			"flush":          e.ioFlush,
			"sync":           e.ioSync,
			"dataSync":       e.ioDataSync,
			"lock":           e.ioLock,
			"tryLock":        e.ioTryLock,
			"unlock":         e.ioUnlock,
			"close":          e.ioClose,
			"readCSV":        ioReadCSV,
			"writeCSV":       ioWriteCSV,
			"stat":           ioStat,
			"chmod":          ioChmod,
			"chown":          ioChown,
			"mkdir":          ioMkdir,
			"remove":         ioRemove,
			"rename":         ioRename,
			"symlink":        ioSymlink,
			"readLink":       ioReadLink,
			"listDir":        ioListDir,
			"walkDir":        ioWalkDir,
			"buffer":         e.ioBuffer,
			"bufferToString": e.ioBufferToString,
			"bufferReset":    e.ioBufferReset,
			"toString":       e.ioToString,
			"captureStdout":  e.ioCaptureStdout,
			"captureStderr":  e.ioCaptureStderr,
			"redirectStdout": e.ioRedirectStdout,
			"redirectStderr": e.ioRedirectStderr,
			"redirectStdin":  e.ioRedirectStdin,
			"stdoutWrite": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				text, err := singleStringValue(call, args)
				if err != nil {
					return nil, err
				}
				_, err = io.WriteString(e.stdout, text)
				return runtime.Null{}, err
			},
			"stderrWrite": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				text, err := singleStringValue(call, args)
				if err != nil {
					return nil, err
				}
				_, err = io.WriteString(e.stderr, text)
				return runtime.Null{}, err
			},
			"stderrPrintln": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				text, err := singlePrintableValue(call, args)
				if err != nil {
					return nil, err
				}
				_, err = fmt.Fprintln(e.stderr, text)
				return runtime.Null{}, err
			},
			"stdinReadAll":  e.ioStdinReadAll,
			"stdinReadLine": e.ioStdinReadLine,
			"readLine":      e.ioReadLine,
			"readLines":     e.ioReadLines,
		},
		"sys": {
			"exit": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				code, err := singleIntValue(call, args)
				if err != nil {
					return nil, err
				}
				return exitValue{code: int(code)}, nil
			},
			"cwd":                sysCwd,
			"getenv":             sysGetenv,
			"args":               e.sysArgs,
			"run":                sysRun,
			"runWithOptions":     sysRunWithOptions,
			"shell":              sysShell,
			"start":              e.sysStart,
			"startWithOptions":   e.sysStartWithOptions,
			"processWrite":       e.sysProcessWrite,
			"processCloseStdin":  e.sysProcessCloseStdin,
			"processReadStdout":  e.sysProcessReadStdout,
			"processReadStderr":  e.sysProcessReadStderr,
			"processReadStdoutN": e.sysProcessReadStdoutN,
			"processReadStderrN": e.sysProcessReadStderrN,
			"processWait":        e.sysProcessWait,
			"processKill":        e.sysProcessKill,
			"processSignal":      e.sysProcessSignal,
			"processPid":         e.sysProcessPid,
			"setenv":             sysSetenv,
			"sleep":              sysSleep,
			"hostname":           e.registryBuiltin("sys", "hostname"),
			"pid":                e.registryBuiltin("sys", "pid"),
			"platform":           e.registryBuiltin("sys", "platform"),
			"arch":               e.registryBuiltin("sys", "arch"),
			"tmpdir":             e.registryBuiltin("sys", "tmpdir"),
			"homedir":            e.registryBuiltin("sys", "homedir"),
			"username":           e.registryBuiltin("sys", "username"),
			"environ":            e.registryBuiltin("sys", "environ"),
		},
		"process": {
			"run": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				spec, err := processSpecFromCombinedArgs(call, args)
				if err != nil {
					return nil, err
				}
				raw, err := runProcessSpec(spec)
				if err != nil {
					return nil, err
				}
				return e.newProcessResult(raw)
			},
			"runWithOptions": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				spec, err := processSpecFromOptions(call, args)
				if err != nil {
					return nil, err
				}
				raw, err := runProcessSpec(spec)
				if err != nil {
					return nil, err
				}
				return e.newProcessResult(raw)
			},
			"shell": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("%s expects 1 argument", call.Callee.String())
				}
				cmd, ok := args[0].(runtime.String)
				if !ok {
					return nil, fmt.Errorf("%s argument must be string", call.Callee.String())
				}
				raw, err := runProcessSpec(processSpec{command: "sh", args: []string{"-c", cmd.Value}})
				if err != nil {
					return nil, err
				}
				return e.newProcessResult(raw)
			},
			"start": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				spec, err := processSpecFromCombinedArgs(call, args)
				if err != nil {
					return nil, err
				}
				handle, err := e.startProcessSpec(spec)
				if err != nil {
					return nil, err
				}
				return &runtime.Instance{Class: e.processClass, Fields: map[string]runtime.Value{"handle": handle}}, nil
			},
			"startWithOptions": func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				spec, err := processSpecFromOptions(call, args)
				if err != nil {
					return nil, err
				}
				handle, err := e.startProcessSpec(spec)
				if err != nil {
					return nil, err
				}
				return &runtime.Instance{Class: e.processClass, Fields: map[string]runtime.Value{"handle": handle}}, nil
			},
		},
		"procnative": {
			"spawn":  e.procSpawn,
			"wait":   e.procWait,
			"kill":   e.procKill,
			"signal": e.procSignal,
			"pid":    e.procPid,
		},
		"sshnative": {
			"connect":       e.sshConnect,
			"exec":          e.sshExec,
			"spawn":         e.sshSpawn,
			"sessionWait":   e.sshSessionWait,
			"sessionKill":   e.sshSessionKill,
			"upload":        e.sshUpload,
			"download":      e.sshDownload,
			"sftpList":      e.sshSftpList,
			"sftpRemove":    e.sshSftpRemove,
			"sftpMkdir":     e.sshSftpMkdir,
			"sftpOpen":      e.sshSftpOpen,
			"forwardLocal":  e.sshForwardLocal,
			"forwardRemote": e.sshForwardRemote,
			"tunnelClose":   e.sshTunnelClose,
			"close":         e.sshClose,
		},
		"async": {
			"run":     e.asyncRun,
			"sleep":   asyncSleep,
			"await":   asyncAwait,
			"done":    asyncDone,
			"all":     asyncAll,
			"race":    asyncRace,
			"timeout": asyncTimeout,
			"cancel":  asyncCancel,
			"token":   asyncToken,
		},
		"amqp": {
			"dial":            e.amqpDial,
			"channel":         e.amqpChannel,
			"declareQueue":    e.amqpDeclareQueue,
			"declareExchange": e.amqpDeclareExchange,
			"queueBind":       e.amqpQueueBind,
			"publish":         e.amqpPublish,
			"get":             e.amqpGet,
			"ack":             e.amqpAck,
			"close":           e.amqpClose,
		},
		"kafka": {
			"writer": e.kafkaWriter,
			"write":  e.kafkaWrite,
			"reader": e.kafkaReader,
			"read":   e.kafkaRead,
			"commit": e.kafkaCommit,
			"close":  e.kafkaClose,
		},
		"json": {
			"parse":            e.registryBuiltin("json", "parse"),
			"parseAs":          e.registryBuiltin("json", "parseAs"),
			"stringify":        e.registryBuiltin("json", "stringify"),
			"validate":         e.registryBuiltin("json", "validate"),
			"tryParse":         e.registryBuiltin("json", "tryParse"),
			"validateDetailed": e.registryBuiltin("json", "validateDetailed"),
			"reader":           e.jsonReader,
			"stream":           e.jsonStream,
		},
		"xml": {
			"parse":            e.registryBuiltin("xml", "parse"),
			"parseAs":          e.registryBuiltin("xml", "parseAs"),
			"tryParse":         e.registryBuiltin("xml", "tryParse"),
			"stringify":        e.registryBuiltin("xml", "stringify"),
			"validate":         e.registryBuiltin("xml", "validate"),
			"validateDetailed": e.registryBuiltin("xml", "validateDetailed"),
			"reader":           e.xmlReader,
			"stream":           e.xmlStream,
		},
		"toml": {
			"parse":            e.registryBuiltin("toml", "parse"),
			"parseAs":          e.registryBuiltin("toml", "parseAs"),
			"tryParse":         e.registryBuiltin("toml", "tryParse"),
			"stringify":        e.registryBuiltin("toml", "stringify"),
			"validate":         e.registryBuiltin("toml", "validate"),
			"validateDetailed": e.registryBuiltin("toml", "validateDetailed"),
		},
		"yaml": {
			"parse":            e.registryBuiltin("yaml", "parse"),
			"parseAs":          e.registryBuiltin("yaml", "parseAs"),
			"tryParse":         e.registryBuiltin("yaml", "tryParse"),
			"stringify":        e.registryBuiltin("yaml", "stringify"),
			"validate":         e.registryBuiltin("yaml", "validate"),
			"validateDetailed": e.registryBuiltin("yaml", "validateDetailed"),
			"reader":           e.yamlReader,
			"stream":           e.yamlStream,
		},
		"csv": {
			"reader":    e.csvReader,
			"stream":    e.csvStream,
			"parse":     e.registryBuiltin("csv", "parse"),
			"parseDict": e.registryBuiltin("csv", "parseDict"),
			"stringify": e.registryBuiltin("csv", "stringify"),
		},
		"bytes": {
			"fromString":    e.registryBuiltin("bytes", "fromString"),
			"fromList":      e.registryBuiltin("bytes", "fromList"),
			"toString":      e.registryBuiltin("bytes", "toString"),
			"fromHex":       e.registryBuiltin("bytes", "fromHex"),
			"toHex":         e.registryBuiltin("bytes", "toHex"),
			"fromBase64":    e.registryBuiltin("bytes", "fromBase64"),
			"toBase64":      e.registryBuiltin("bytes", "toBase64"),
			"fromBase64Url": e.registryBuiltin("bytes", "fromBase64Url"),
			"toBase64Url":   e.registryBuiltin("bytes", "toBase64Url"),
			"concat":        e.registryBuiltin("bytes", "concat"),
		},
		"string": {
			"fromCodePoint":  e.registryBuiltin("string", "fromCodePoint"),
			"fromCodePoints": e.registryBuiltin("string", "fromCodePoints"),
			"compare":        e.registryBuiltin("string", "compare"),
			"equalsFold":     e.registryBuiltin("string", "equalsFold"),
		},
		"collections": {
			"length":          collectionsLength,
			"isEmpty":         collectionsIsEmpty,
			"contains":        collectionsContains,
			"reverse":         collectionsReverse,
			"sort":            collectionsSort,
			"join":            collectionsJoin,
			"range":           collectionsRange,
			"take":            collectionsTake,
			"lazyMap":         e.collectionsLazyMap,
			"lazyFilter":      e.collectionsLazyFilter,
			"map":             e.collectionsMethod("map", 2),
			"filter":          e.collectionsMethod("filter", 2),
			"reduce":          e.collectionsMethod("reduce", 3),
			"find":            e.collectionsMethod("find", 2),
			"any":             e.collectionsMethod("any", 2),
			"all":             e.collectionsMethod("all", 2),
			"flatten":         e.collectionsMethod("flatten", 1),
			"unique":          e.collectionsMethod("unique", 1),
			"zip":             e.collectionsMethod("zip", 2),
			"sorted":          e.collectionsMethod("sorted", -1),
			"groupBy":         e.collectionsMethod("groupBy", 2),
			"chunk":           e.collectionsMethod("chunk", 2),
			"partition":       e.collectionsMethod("partition", 2),
			"findLast":        e.collectionsMethod("findLast", 2),
			"containsBy":      e.collectionsMethod("containsBy", 3),
			"indexBy":         e.collectionsMethod("indexBy", 2),
			"binarySearch":    e.collectionsMethod("binarySearch", 2),
			"lowerBound":      e.collectionsMethod("lowerBound", 2),
			"upperBound":      e.collectionsMethod("upperBound", 2),
			"minBy":           e.collectionsMethod("minBy", 2),
			"maxBy":           e.collectionsMethod("maxBy", 2),
			"sortBy":          e.collectionsMethod("sortBy", 2),
			"topBy":           e.collectionsMethod("topBy", 3),
			"sumBy":           e.collectionsMethod("sumBy", 2),
			"averageBy":       e.collectionsMethod("averageBy", 2),
			"topK":            e.collectionsMethod("topK", 2),
			"bottomK":         e.collectionsMethod("bottomK", 2),
			"frequencies":     e.collectionsMethod("frequencies", 1),
			"mode":            e.collectionsMethod("mode", 1),
			"difference":      e.collectionsMethod("difference", 2),
			"intersection":    e.collectionsMethod("intersection", 2),
			"differenceBy":    e.collectionsMethod("differenceBy", 3),
			"intersectionBy":  e.collectionsMethod("intersectionBy", 3),
			"zipWith":         e.collectionsMethod("zipWith", 3),
			"flatMap":         e.collectionsMethod("flatMap", 2),
			"uniqueBy":        e.collectionsMethod("uniqueBy", 2),
			"takeWhile":       e.collectionsMethod("takeWhile", 2),
			"dropWhile":       e.collectionsMethod("dropWhile", 2),
			"windowed":        e.collectionsMethod("windowed", -1),
			"unzip":           e.collectionsMethod("unzip", 1),
			"scan":            e.collectionsMethod("scan", 3),
			"enumerate":       e.collectionsMethod("enumerate", 1),
			"bfs":             e.collectionsGraphMethod("bfs", 1),
			"dfs":             e.collectionsGraphMethod("dfs", 1),
			"topologicalSort": e.collectionsGraphMethod("topologicalSort", 0),
			"shortestPath":    e.collectionsGraphMethod("shortestPath", 2),
		},
		"secrets": {
			"getEnv":            secretsGetEnv,
			"requireEnv":        secretsRequireEnv,
			"readFile":          secretsReadFile,
			"randomBytes":       e.registryBuiltin("secrets", "randomBytes"),
			"randomInt":         e.registryBuiltin("secrets", "randomInt"),
			"randomHex":         e.registryBuiltin("secrets", "randomHex"),
			"randomBase64":      e.registryBuiltin("secrets", "randomBase64"),
			"constantTimeEqual": e.registryBuiltin("secrets", "constantTimeEqual"),
		},
		"random": {
			"seed":      e.registryBuiltin("random", "seed"),
			"next":      e.registryBuiltin("random", "next"),
			"intRange":  e.registryBuiltin("random", "intRange"),
			"float":     e.registryBuiltin("random", "float"),
			"bool":      e.registryBuiltin("random", "bool"),
			"choice":    e.registryBuiltin("random", "choice"),
			"shuffle":   e.registryBuiltin("random", "shuffle"),
			"Generator": e.registryBuiltin("random", "Generator"),
		},
		"strbuilder": {
			"new":        e.registryBuiltin("strbuilder", "new"),
			"append":     e.registryBuiltin("strbuilder", "append"),
			"appendLine": e.registryBuiltin("strbuilder", "appendLine"),
			"build":      e.registryBuiltin("strbuilder", "build"),
			"length":     e.registryBuiltin("strbuilder", "length"),
			"clear":      e.registryBuiltin("strbuilder", "clear"),
			"dispose":    e.registryBuiltin("strbuilder", "dispose"),
		},
		"time": {
			"now":          e.registryBuiltin("time", "now"),
			"elapsed":      e.registryBuiltin("time", "elapsed"),
			"sleep":        e.registryBuiltin("time", "sleep"),
			"monotonic":    e.registryBuiltin("time", "monotonic"),
			"unix":         e.registryBuiltin("time", "unix"),
			"unixMilli":    e.registryBuiltin("time", "unixMilli"),
			"unixMicro":    e.registryBuiltin("time", "unixMicro"),
			"unixNano":     e.registryBuiltin("time", "unixNano"),
			"unixFloat":    e.registryBuiltin("time", "unixFloat"),
			"unixDecimal":  e.registryBuiltin("time", "unixDecimal"),
			"elapsedFloat": e.registryBuiltin("time", "elapsedFloat"),
			"humanize":     e.registryBuiltin("time", "humanize"),
		},
		"profiler": {
			"snapshot": e.registryBuiltin("profiler", "snapshot"),
			"delta":    e.registryBuiltin("profiler", "delta"),
			"memory":   e.registryBuiltin("profiler", "memory"),
			"cpu":      e.registryBuiltin("profiler", "cpu"),
			"peak":     e.registryBuiltin("profiler", "peak"),
		},
		"schema": {
			"validate": schemaValidate,
		},
		"serde": {
			"parse":     e.serdeParse,
			"stringify": e.serdeStringify,
		},
		"metrics": {
			"inc":          e.metricsInc,
			"set":          e.metricsSet,
			"get":          e.metricsGet,
			"snapshot":     e.metricsSnapshot,
			"reset":        e.metricsReset,
			"now":          metricsNow,
			"duration":     metricsDuration,
			"counter":      e.metricsCounter,
			"gauge":        e.metricsGauge,
			"histogram":    e.metricsHistogram,
			"observe":      e.metricsObserve,
			"toPrometheus": e.metricsToPrometheus,
		},
		"trace": {
			"start":      e.traceStart,
			"event":      e.traceEvent,
			"end":        e.traceEnd,
			"snapshot":   e.traceSnapshot,
			"reset":      e.traceReset,
			"toOtlpJson": e.traceToOtlpJson,
			"exportOtlp": e.traceExportOtlp,
		},
		"profile": {
			"memStats": profileMemStats,
			"gc":       profileGC,
			"now":      metricsNow,
			"elapsed":  metricsDuration,
		},
		"web": {
			"new":            e.webNew,
			"use":            e.webUse,
			"before":         e.webBefore,
			"after":          e.webUse,
			"route":          e.webRoute,
			"get":            e.webGet,
			"post":           e.webPost,
			"handle":         e.webHandle,
			"withHeader":     webWithHeader,
			"parseMultipart": webParseMultipart,
		},
		"crypt": {
			"md5":                    e.registryBuiltin("crypt", "md5"),
			"sha1":                   e.registryBuiltin("crypt", "sha1"),
			"sha256":                 e.registryBuiltin("crypt", "sha256"),
			"sha512":                 e.registryBuiltin("crypt", "sha512"),
			"sha3_256":               e.registryBuiltin("crypt", "sha3_256"),
			"blake2b":                e.registryBuiltin("crypt", "blake2b"),
			"crc32":                  e.registryBuiltin("crypt", "crc32"),
			"hmacSha256":             e.registryBuiltin("crypt", "hmacSha256"),
			"hmacSha256Bytes":        e.registryBuiltin("crypt", "hmacSha256Bytes"),
			"bcryptHash":             e.registryBuiltin("crypt", "bcryptHash"),
			"bcryptVerify":           e.registryBuiltin("crypt", "bcryptVerify"),
			"argon2idHash":           e.registryBuiltin("crypt", "argon2idHash"),
			"argon2idVerify":         e.registryBuiltin("crypt", "argon2idVerify"),
			"passwordHash":           e.registryBuiltin("crypt", "passwordHash"),
			"passwordVerify":         e.registryBuiltin("crypt", "passwordVerify"),
			"randomHex":              e.registryBuiltin("crypt", "randomHex"),
			"base64Encode":           e.registryBuiltin("crypt", "base64Encode"),
			"base64Decode":           e.registryBuiltin("crypt", "base64Decode"),
			"jwtSign":                e.registryBuiltin("crypt", "jwtSign"),
			"jwtVerify":              e.registryBuiltin("crypt", "jwtVerify"),
			"jwtDecode":              e.registryBuiltin("crypt", "jwtDecode"),
			"generateRsaKey":         e.registryBuiltin("crypt", "generateRsaKey"),
			"generateEcKey":          e.registryBuiltin("crypt", "generateEcKey"),
			"generateEd25519Key":     e.registryBuiltin("crypt", "generateEd25519Key"),
			"publicKey":              e.registryBuiltin("crypt", "publicKey"),
			"generateSelfSignedCert": e.registryBuiltin("crypt", "generateSelfSignedCert"),
			"generateCsr":            e.registryBuiltin("crypt", "generateCsr"),
			"parseCert":              e.registryBuiltin("crypt", "parseCert"),
			"signCertificate":        e.registryBuiltin("crypt", "signCertificate"),
			"pkcs12Decode":           e.registryBuiltin("crypt", "pkcs12Decode"),
			"jweEncrypt":             e.registryBuiltin("crypt", "jweEncrypt"),
			"jweDecrypt":             e.registryBuiltin("crypt", "jweDecrypt"),
			"jwtSignRS256":           e.registryBuiltin("crypt", "jwtSignRS256"),
			"jwtVerifyRS256":         e.registryBuiltin("crypt", "jwtVerifyRS256"),
			"jwtSignES256":           e.registryBuiltin("crypt", "jwtSignES256"),
			"jwtVerifyES256":         e.registryBuiltin("crypt", "jwtVerifyES256"),
			"aesEncrypt":             e.registryBuiltin("crypt", "aesEncrypt"),
			"aesDecrypt":             e.registryBuiltin("crypt", "aesDecrypt"),
			"chacha20Encrypt":        e.registryBuiltin("crypt", "chacha20Encrypt"),
			"chacha20Decrypt":        e.registryBuiltin("crypt", "chacha20Decrypt"),
		},
		"binary": {
			"pack":        e.registryBuiltin("binary", "pack"),
			"unpack":      e.registryBuiltin("binary", "unpack"),
			"unpackNamed": e.registryBuiltin("binary", "unpackNamed"),
			"size":        e.registryBuiltin("binary", "size"),
		},
		"encoding": {
			"base64Encode":    e.registryBuiltin("encoding", "base64Encode"),
			"base64Decode":    e.registryBuiltin("encoding", "base64Decode"),
			"base32Encode":    e.registryBuiltin("encoding", "base32Encode"),
			"base32Decode":    e.registryBuiltin("encoding", "base32Decode"),
			"base58Encode":    e.registryBuiltin("encoding", "base58Encode"),
			"base58Decode":    e.registryBuiltin("encoding", "base58Decode"),
			"base64UrlEncode": e.registryBuiltin("encoding", "base64UrlEncode"),
			"base64UrlDecode": e.registryBuiltin("encoding", "base64UrlDecode"),
			"urlEncode":       e.registryBuiltin("encoding", "urlEncode"),
			"urlDecode":       e.registryBuiltin("encoding", "urlDecode"),
			"htmlEscape":      e.registryBuiltin("encoding", "htmlEscape"),
			"htmlUnescape":    e.registryBuiltin("encoding", "htmlUnescape"),
		},
		"compress": {
			"gzip":   e.registryBuiltin("compress", "gzip"),
			"gunzip": e.registryBuiltin("compress", "gunzip"),
		},
		"archive": {
			"zipRead":    e.registryBuiltin("archive", "zipRead"),
			"zipWrite":   e.registryBuiltin("archive", "zipWrite"),
			"tarRead":    e.registryBuiltin("archive", "tarRead"),
			"tarWrite":   e.registryBuiltin("archive", "tarWrite"),
			"tarGzRead":  e.registryBuiltin("archive", "tarGzRead"),
			"tarGzWrite": e.registryBuiltin("archive", "tarGzWrite"),
		},
		"url": {
			"URL":       e.registryBuiltin("url", "URL"),
			"parse":     e.registryBuiltin("url", "parse"),
			"stringify": e.registryBuiltin("url", "stringify"),
			"encode":    e.registryBuiltin("url", "encode"),
			"decode":    e.registryBuiltin("url", "decode"),
			"joinPath":  e.registryBuiltin("url", "joinPath"),
		},
		"template": {
			"renderString": e.registryBuiltin("template", "renderString"),
			"Template":     e.registryBuiltin("template", "Template"),
			"load":         e.registryBuiltin("template", "load"),
			"Engine":       e.registryBuiltin("template", "Engine"),
		},
		"re": {
			"test":     e.registryBuiltin("re", "test"),
			"find":     e.registryBuiltin("re", "find"),
			"findAll":  e.registryBuiltin("re", "findAll"),
			"match":    e.registryBuiltin("re", "match"),
			"matchAll": e.registryBuiltin("re", "matchAll"),
			"replace":  e.registryBuiltin("re", "replace"),
			"split":    e.registryBuiltin("re", "split"),
		},
		"pcre": {
			"test":     e.registryBuiltin("pcre", "test"),
			"find":     e.registryBuiltin("pcre", "find"),
			"findAll":  e.registryBuiltin("pcre", "findAll"),
			"match":    e.registryBuiltin("pcre", "match"),
			"matchAll": e.registryBuiltin("pcre", "matchAll"),
			"replace":  e.registryBuiltin("pcre", "replace"),
			"split":    e.registryBuiltin("pcre", "split"),
			"quote":    e.registryBuiltin("pcre", "quote"),
		},
		"markdown": {
			"parse":      e.registryBuiltin("markdown", "parse"),
			"renderHtml": e.registryBuiltin("markdown", "renderHtml"),
			"stripText":  e.registryBuiltin("markdown", "stripText"),
		},
		"datetime": {
			"nowUnix":       e.registryBuiltin("datetime", "nowUnix"),
			"unix":          e.registryBuiltin("datetime", "unix"),
			"parse":         e.registryBuiltin("datetime", "parse"),
			"format":        e.registryBuiltin("datetime", "format"),
			"addSeconds":    e.registryBuiltin("datetime", "addSeconds"),
			"addDays":       e.registryBuiltin("datetime", "addDays"),
			"addMonths":     e.registryBuiltin("datetime", "addMonths"),
			"addYears":      e.registryBuiltin("datetime", "addYears"),
			"diff":          e.registryBuiltin("datetime", "diff"),
			"toLocal":       e.registryBuiltin("datetime", "toLocal"),
			"toUtc":         e.registryBuiltin("datetime", "toUtc"),
			"now":           e.registryBuiltin("datetime", "now"),
			"nowInstant":    e.registryBuiltin("datetime", "nowInstant"),
			"Instant":       e.registryBuiltin("datetime", "Instant"),
			"Duration":      e.registryBuiltin("datetime", "Duration"),
			"Zone":          e.registryBuiltin("datetime", "Zone"),
			"sleep":         sysSleep,
			"make":          e.registryBuiltin("datetime", "make"),
			"formatRFC3339": e.registryBuiltin("datetime", "formatRFC3339"),
			"formatDate":    e.registryBuiltin("datetime", "formatDate"),
			"formatTime":    e.registryBuiltin("datetime", "formatTime"),
			"formatHTTP":    e.registryBuiltin("datetime", "formatHTTP"),
			"parseRFC3339":  e.registryBuiltin("datetime", "parseRFC3339"),
			"partsInZone":   e.registryBuiltin("datetime", "partsInZone"),
			"weekdayName":   e.registryBuiltin("datetime", "weekdayName"),
			"monthName":     e.registryBuiltin("datetime", "monthName"),
		},
		"uuid": {
			"v1":            e.registryBuiltin("uuid", "v1"),
			"v4":            e.registryBuiltin("uuid", "v4"),
			"v7":            e.registryBuiltin("uuid", "v7"),
			"v3":            e.registryBuiltin("uuid", "v3"),
			"v5":            e.registryBuiltin("uuid", "v5"),
			"parse":         e.registryBuiltin("uuid", "parse"),
			"isValid":       e.registryBuiltin("uuid", "isValid"),
			"nil":           e.registryBuiltin("uuid", "nil"),
			"toBytes":       e.registryBuiltin("uuid", "toBytes"),
			"fromBytes":     e.registryBuiltin("uuid", "fromBytes"),
			"namespaceDNS":  e.registryBuiltin("uuid", "namespaceDNS"),
			"namespaceURL":  e.registryBuiltin("uuid", "namespaceURL"),
			"namespaceOID":  e.registryBuiltin("uuid", "namespaceOID"),
			"namespaceX500": e.registryBuiltin("uuid", "namespaceX500"),
			"ulid":          e.registryBuiltin("uuid", "ulid"),
		},
		"args": {
			"parse": e.registryBuiltin("args", "parse"),
			"help":  e.registryBuiltin("args", "help"),
		},
		"dotenv": {
			"parse":        e.dotenvParse,
			"load":         e.dotenvLoad,
			"apply":        e.dotenvApply,
			"loadAndApply": e.dotenvLoadAndApply,
		},
		"cli": {
			"prompt":         e.cliPrompt,
			"password":       e.cliPassword,
			"secret":         e.cliPassword,
			"confirm":        e.cliConfirm,
			"choose":         e.cliChoose,
			"style":          cliStyle,
			"stripAnsi":      cliStripANSI,
			"table":          cliTable,
			"parseArgs":      e.registryBuiltin("args", "parse"),
			"help":           e.registryBuiltin("args", "help"),
			"spinnerTick":    e.cliSpinnerTick,
			"spinnerStop":    e.cliSpinnerStop,
			"progressRender": e.cliProgressRender,
			"progressFinish": e.cliProgressFinish,
		},
		"http": {
			"serve":              e.httpServe,
			"listen":             e.httpListen,
			"close":              e.httpClose,
			"shutdown":           e.httpShutdown,
			"serverAddr":         e.httpServerAddr,
			"serverCert":         e.httpServerCert,
			"serverStats":        e.httpServerStats,
			"stream":             httpStreamResponse,
			"streamWrite":        e.httpStreamWrite,
			"streamFlush":        e.httpStreamFlush,
			"streamClose":        e.httpStreamClose,
			"get":                httpGet,
			"post":               e.httpPost,
			"postJson":           httpPostJSON,
			"parseJson":          httpParseJSON,
			"request":            e.httpRequest,
			"requestWithOptions": e.httpRequestWithOptions,
			"Headers":            httpHeadersObject,
			"Cookie":             httpCookieObject,
			"response":           e.httpResponseObject,
			"jsonResponse":       e.httpJSONResponseObject,
			"newClient":          e.httpNewClient,
			"newCookieJar":       e.httpNewCookieJar,
			"build":              e.httpBuild,
			"fetchAll":           e.httpFetchAll,
			"fetchStream":        e.httpFetchStream,
		},
		"websocket": {
			"connect":   e.websocketConnect,
			"upgrade":   websocketUpgradeResponse,
			"sendText":  e.websocketSendText,
			"readText":  e.websocketReadText,
			"sendBytes": e.websocketSendBytes,
			"readBytes": e.websocketReadBytes,
			"close":     e.websocketClose,
		},
		"smtp": {
			"message": smtpMessageBuiltin,
			"send":    smtpSendBuiltin,
		},
		"db": {
			"Connection": e.dbConnectionObject,
			"connect":    e.dbConnectionObject,
			"open":       e.dbOpen,
			"exec":       e.dbExec,
			"query":      e.dbQuery,
			"begin":      e.dbBegin,
			"txExec":     e.dbTxExec,
			"txQuery":    e.dbTxQuery,
			"commit":     e.dbCommit,
			"rollback":   e.dbRollback,
			"prepare":    e.dbPrepare,
			"stmtExec":   e.dbStmtExec,
			"stmtQuery":  e.dbStmtQuery,
			"stmtClose":  e.dbStmtClose,
			"configure":  e.dbConfigure,
			"stats":      e.dbStats,
			"migrate":    e.dbMigrate,
			"close":      e.dbClose,
		},
		"ext":       e.extBuiltins(),
		"ffinative": e.ffiBuiltins(),
		"net": {
			"joinHostPort":  netJoinHostPort,
			"splitHostPort": netSplitHostPort,
			"lookupHost":    netLookupHost,
			"parseIp":       netParseIP,
			"parseCidr":     netParseCIDR,
			"cidrContains":  netCIDRContains,
			"cidrRange":     netCIDRRange,
			"isIpv4":        netIsIPv4,
			"isIpv6":        netIsIPv6,
			"ipToBytes":     netIPToBytes,
			"ipFromBytes":   netIPFromBytes,
			"listenTcp":     e.netListenTCP,
			"connectTcp":    e.netConnectTCP,
			"accept":        e.netAccept,
			"read":          e.netRead,
			"write":         e.netWrite,
			"setDeadline":   e.netSetDeadline,
			"clearDeadline": e.netClearDeadline,
			"close":         e.netClose,
			"localAddr":     e.netLocalAddr,
			"remoteAddr":    e.netRemoteAddr,
			"listenUdp":     e.netListenUDP,
			"dialUdp":       e.netDialUDP,
			"readFrom":      e.netReadFrom,
			"writeTo":       e.netWriteTo,
			"dial":          e.netDial,
			"serve":         e.netServe,
			"closeListener": e.netCloseListener,
			"serverStats":   e.netServerStats,
		},
		"test": {
			"run":        e.testRun,
			"mock":       e.testMock,
			"restore":    e.testRestore,
			"restoreAll": e.testRestoreAll,
		},
		"reflect": {
			"decorators":       reflectDecorators,
			"hasDecorator":     reflectHasDecorator,
			"decorator":        reflectDecorator,
			"parameters":       reflectParameters,
			"returnType":       reflectReturnType,
			"doc":              reflectDoc,
			"docs":             reflectDocs,
			"typeOf":           reflectTypeOf,
			"location":         reflectLocation,
			"exports":          reflectExports,
			"fields":           reflectFields,
			"getField":         reflectGetField,
			"setField":         reflectSetField,
			"methods":          reflectMethods,
			"staticMethods":    reflectStaticMethods,
			"parent":           reflectParent,
			"className":        reflectClassName,
			"interfaces":       reflectInterfaces,
			"constructors":     reflectConstructors,
			"typeBindings":     reflectTypeBindings,
			"interfaceMethods": reflectInterfaceMethods,
			"interfaceParents": reflectInterfaceParents,
			"function":         reflectLookupRequiresEvaluator,
			"class":            reflectLookupRequiresEvaluator,
			"module":           reflectLookupRequiresEvaluator,
			"method":           e.reflectMethodBound,
			"staticMethod":     reflectStaticMethod,
		},
		"log": {
			"stdout":   e.logStdout,
			"stderr":   e.logStderr,
			"file":     e.logFile,
			"toStream": e.logToStream,
			"custom":   e.logCustom,
			"info":     e.logInfo,
			"warn":     e.logWarn,
			"error":    e.logError,
			"debug":    e.logDebug,
			"close":    e.logClose,
		},
		"path": {
			"join":  pathJoin,
			"clean": pathClean,
			"base":  pathBase,
			"dir":   pathDir,
			"ext":   pathExt,
			"abs":   pathAbs,
			"rel":   pathRel,
			"glob":  pathGlob,
		},
		"watch": {
			"snapshot": watchSnapshot,
			"wait":     watchWait,
			"start":    e.watchStart,
			"stop":     e.watchStop,
		},
		"math": {
			"abs":        e.registryBuiltin("math", "abs"),
			"min":        e.registryBuiltin("math", "min"),
			"max":        e.registryBuiltin("math", "max"),
			"clamp":      e.registryBuiltin("math", "clamp"),
			"floor":      e.registryBuiltin("math", "floor"),
			"ceil":       e.registryBuiltin("math", "ceil"),
			"round":      e.registryBuiltin("math", "round"),
			"sqrt":       e.registryBuiltin("math", "sqrt"),
			"sin":        e.registryBuiltin("math", "sin"),
			"cos":        e.registryBuiltin("math", "cos"),
			"tan":        e.registryBuiltin("math", "tan"),
			"asin":       e.registryBuiltin("math", "asin"),
			"acos":       e.registryBuiltin("math", "acos"),
			"atan":       e.registryBuiltin("math", "atan"),
			"atan2":      e.registryBuiltin("math", "atan2"),
			"log":        e.registryBuiltin("math", "log"),
			"log10":      e.registryBuiltin("math", "log10"),
			"exp":        e.registryBuiltin("math", "exp"),
			"pow":        e.registryBuiltin("math", "pow"),
			"pi":         e.registryBuiltin("math", "pi"),
			"e":          e.registryBuiltin("math", "e"),
			"log2":       e.registryBuiltin("math", "log2"),
			"trunc":      e.registryBuiltin("math", "trunc"),
			"sign":       e.registryBuiltin("math", "sign"),
			"cbrt":       e.registryBuiltin("math", "cbrt"),
			"hypot":      e.registryBuiltin("math", "hypot"),
			"inf":        e.registryBuiltin("math", "inf"),
			"nan":        e.registryBuiltin("math", "nan"),
			"isNaN":      e.registryBuiltin("math", "isNaN"),
			"isInf":      e.registryBuiltin("math", "isInf"),
			"isPrime":    e.registryBuiltin("math", "isPrime"),
			"median":     e.registryBuiltin("math", "median"),
			"percentile": e.registryBuiltin("math", "percentile"),
			"quantile":   e.registryBuiltin("math", "quantile"),
			"mode":       e.registryBuiltin("math", "mode"),
			"tau":        e.registryBuiltin("math", "tau"),
			"ln2":        e.registryBuiltin("math", "ln2"),
			"ln10":       e.registryBuiltin("math", "ln10"),
			"sqrt2":      e.registryBuiltin("math", "sqrt2"),
			"phi":        e.registryBuiltin("math", "phi"),
			"maxInt":     e.registryBuiltin("math", "maxInt"),
			"minInt":     e.registryBuiltin("math", "minInt"),
			"maxFloat":   e.registryBuiltin("math", "maxFloat"),
			"minFloat":   e.registryBuiltin("math", "minFloat"),
			"epsilon":    e.registryBuiltin("math", "epsilon"),
			"sqrt2Pi":    e.registryBuiltin("math", "sqrt2Pi"),
			"log2Pi":     e.registryBuiltin("math", "log2Pi"),
		},
		"secureRandom": {
			"openSession":      e.registryBuiltin("secureRandom", "openSession"),
			"fromSeed":         e.registryBuiltin("secureRandom", "fromSeed"),
			"commitment":       e.registryBuiltin("secureRandom", "commitment"),
			"reveal":           e.registryBuiltin("secureRandom", "reveal"),
			"auditLog":         e.registryBuiltin("secureRandom", "auditLog"),
			"auditLogJson":     e.registryBuiltin("secureRandom", "auditLogJson"),
			"bytes":            e.registryBuiltin("secureRandom", "bytes"),
			"uintRange":        e.registryBuiltin("secureRandom", "uintRange"),
			"float":            e.registryBuiltin("secureRandom", "float"),
			"bool":             e.registryBuiltin("secureRandom", "bool"),
			"choice":           e.registryBuiltin("secureRandom", "choice"),
			"shuffle":          e.registryBuiltin("secureRandom", "shuffle"),
			"weightedChoice":   e.registryBuiltin("secureRandom", "weightedChoice"),
			"verifyCommitment": e.registryBuiltin("secureRandom", "verifyCommitment"),
			"replay":           e.registryBuiltin("secureRandom", "replay"),
		},
		"msgpack": {
			"encode":    e.registryBuiltin("msgpack", "encode"),
			"decode":    e.registryBuiltin("msgpack", "decode"),
			"tryDecode": e.registryBuiltin("msgpack", "tryDecode"),
			"validate":  e.registryBuiltin("msgpack", "validate"),
		},
		"unicode": {
			"normalize":    e.registryBuiltin("unicode", "normalize"),
			"isNormalized": e.registryBuiltin("unicode", "isNormalized"),
		},
		"cron": {
			"parse":     e.registryBuiltin("cron", "parse"),
			"isValid":   e.registryBuiltin("cron", "isValid"),
			"nextAfter": e.registryBuiltin("cron", "nextAfter"),
			"nextN":     e.registryBuiltin("cron", "nextN"),
		},
		"async.sync": {
			"mutexNew":            e.registryBuiltin("async.sync", "mutexNew"),
			"mutexLock":           e.registryBuiltin("async.sync", "mutexLock"),
			"mutexUnlock":         e.registryBuiltin("async.sync", "mutexUnlock"),
			"mutexTryLock":        e.registryBuiltin("async.sync", "mutexTryLock"),
			"rwmutexNew":          e.registryBuiltin("async.sync", "rwmutexNew"),
			"rwmutexLock":         e.registryBuiltin("async.sync", "rwmutexLock"),
			"rwmutexUnlock":       e.registryBuiltin("async.sync", "rwmutexUnlock"),
			"rwmutexTryLock":      e.registryBuiltin("async.sync", "rwmutexTryLock"),
			"rwmutexRLock":        e.registryBuiltin("async.sync", "rwmutexRLock"),
			"rwmutexRUnlock":      e.registryBuiltin("async.sync", "rwmutexRUnlock"),
			"rwmutexTryRLock":     e.registryBuiltin("async.sync", "rwmutexTryRLock"),
			"semaphoreNew":        e.registryBuiltin("async.sync", "semaphoreNew"),
			"semaphoreAcquire":    e.registryBuiltin("async.sync", "semaphoreAcquire"),
			"semaphoreRelease":    e.registryBuiltin("async.sync", "semaphoreRelease"),
			"semaphoreTryAcquire": e.registryBuiltin("async.sync", "semaphoreTryAcquire"),
			"waitgroupNew":        e.registryBuiltin("async.sync", "waitgroupNew"),
			"waitgroupAdd":        e.registryBuiltin("async.sync", "waitgroupAdd"),
			"waitgroupDone":       e.registryBuiltin("async.sync", "waitgroupDone"),
			"waitgroupWait":       e.registryBuiltin("async.sync", "waitgroupWait"),
		},
		"async.atomic": {
			"intNew":             e.registryBuiltin("async.atomic", "intNew"),
			"intLoad":            e.registryBuiltin("async.atomic", "intLoad"),
			"intStore":           e.registryBuiltin("async.atomic", "intStore"),
			"intAdd":             e.registryBuiltin("async.atomic", "intAdd"),
			"intCompareAndSwap":  e.registryBuiltin("async.atomic", "intCompareAndSwap"),
			"boolNew":            e.registryBuiltin("async.atomic", "boolNew"),
			"boolLoad":           e.registryBuiltin("async.atomic", "boolLoad"),
			"boolStore":          e.registryBuiltin("async.atomic", "boolStore"),
			"boolCompareAndSwap": e.registryBuiltin("async.atomic", "boolCompareAndSwap"),
		},
		"async.channel": {
			"make":     e.registryBuiltin("async.channel", "make"),
			"send":     e.registryBuiltin("async.channel", "send"),
			"recv":     e.registryBuiltin("async.channel", "recv"),
			"tryRecv":  e.registryBuiltin("async.channel", "tryRecv"),
			"trySend":  e.registryBuiltin("async.channel", "trySend"),
			"close":    e.registryBuiltin("async.channel", "close"),
			"isClosed": e.registryBuiltin("async.channel", "isClosed"),
		},
		"errors": {
			"new":           e.registryBuiltin("errors", "new"),
			"message":       e.registryBuiltin("errors", "message"),
			"class":         e.registryBuiltin("errors", "class"),
			"stackTrace":    e.registryBuiltin("errors", "stackTrace"),
			"frames":        e.registryBuiltin("errors", "frames"),
			"hasStackTrace": e.registryBuiltin("errors", "hasStackTrace"),
			"is": func(_ *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
				if len(args) != 2 {
					return nil, fmt.Errorf("errors.is expects two arguments")
				}
				err, ok := args[0].(runtime.Error)
				if !ok {
					return nil, fmt.Errorf("errors.is: first argument must be an error, got %s", args[0].TypeName())
				}
				target, ok := args[1].(runtime.String)
				if !ok {
					return nil, fmt.Errorf("errors.is: second argument must be a string class name")
				}
				return runtime.Bool{Value: e.errorTypeMatches(err.Class, target.Value)}, nil
			},
			"wrap": e.registryBuiltin("errors", "wrap"),
		},
		"freeze": {
			"shallow":  e.registryBuiltin("freeze", "shallow"),
			"deep":     e.registryBuiltin("freeze", "deep"),
			"isFrozen": e.registryBuiltin("freeze", "isFrozen"),
		},
		"clone": {
			"deep": e.registryBuiltin("clone", "deep"),
		},
	}
}

func collectionsLength(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	switch value := args[0].(type) {
	case *runtime.List:
		return runtime.SmallInt{Value: int64(len(value.Elements))}, nil
	case runtime.Dict:
		return runtime.SmallInt{Value: int64(len(value.Entries))}, nil
	case runtime.Set:
		return runtime.SmallInt{Value: int64(len(value.Elements))}, nil
	case runtime.String:
		return runtime.SmallInt{Value: int64(len([]rune(value.Value)))}, nil
	case runtime.Bytes:
		return runtime.SmallInt{Value: int64(len(value.Value))}, nil
	default:
		return nil, fmt.Errorf("%s does not support %s", call.Callee.String(), args[0].TypeName())
	}
}

func collectionsIsEmpty(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	length, err := collectionsLength(call, args)
	if err != nil {
		return nil, err
	}
	switch n := length.(type) {
	case runtime.SmallInt:
		return runtime.Bool{Value: n.Value == 0}, nil
	case runtime.Int:
		return runtime.Bool{Value: n.Value.Sign() == 0}, nil
	}
	return runtime.Bool{Value: false}, nil
}

func collectionsContains(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects collection and value", call.Callee.String())
	}
	switch value := args[0].(type) {
	case *runtime.List:
		for _, element := range value.Elements {
			if valuesEqualSimple(element, args[1]) {
				return runtime.Bool{Value: true}, nil
			}
		}
		return runtime.Bool{Value: false}, nil
	case runtime.Dict:
		_, ok := value.Entries[dictKey(args[1])]
		return runtime.Bool{Value: ok}, nil
	case runtime.Set:
		_, ok := value.Elements[dictKey(args[1])]
		return runtime.Bool{Value: ok}, nil
	case runtime.String:
		needle, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s string needle must be string", call.Callee.String())
		}
		return runtime.Bool{Value: strings.Contains(value.Value, needle.Value)}, nil
	case runtime.Bytes:
		needle, ok := args[1].(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("%s bytes needle must be bytes", call.Callee.String())
		}
		return runtime.Bool{Value: bytes.Contains(value.Value, needle.Value)}, nil
	default:
		return nil, fmt.Errorf("%s does not support %s", call.Callee.String(), args[0].TypeName())
	}
}

func collectionsReverse(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	switch value := args[0].(type) {
	case *runtime.List:
		out := make([]runtime.Value, len(value.Elements))
		for i := range value.Elements {
			out[len(value.Elements)-1-i] = value.Elements[i]
		}
		return &runtime.List{Elements: out}, nil
	case runtime.String:
		runes := []rune(value.Value)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return runtime.String{Value: string(runes)}, nil
	case runtime.Bytes:
		out := append([]byte(nil), value.Value...)
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return runtime.Bytes{Value: out}, nil
	default:
		return nil, fmt.Errorf("%s does not support %s", call.Callee.String(), args[0].TypeName())
	}
}

func collectionsSort(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	list, ok := args[0].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s expects list", call.Callee.String())
	}
	out := append([]runtime.Value(nil), list.Elements...)
	var compareErr error
	sort.SliceStable(out, func(i, j int) bool {
		if compareErr != nil {
			return false
		}
		cmp, err := compareValues(out[i], out[j])
		if err != nil {
			compareErr = err
			return false
		}
		return cmp < 0
	})
	if compareErr != nil {
		return nil, compareErr
	}
	return &runtime.List{Elements: out}, nil
}

func collectionsJoin(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects list and separator", call.Callee.String())
	}
	list, ok := args[0].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s expects list", call.Callee.String())
	}
	sep, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s separator must be string", call.Callee.String())
	}
	parts := make([]string, 0, len(list.Elements))
	for _, element := range list.Elements {
		if text, ok := element.(runtime.String); ok {
			parts = append(parts, text.Value)
		} else {
			parts = append(parts, element.Inspect())
		}
	}
	return runtime.String{Value: strings.Join(parts, sep.Value)}, nil
}

type collectionsIterator struct {
	next  func() (runtime.Value, bool, error)
	close func()
}

func collectionsIntArg(value runtime.Value, label string) (*big.Int, error) {
	intValue, ok := value.(runtime.Int)
	if !ok {
		return nil, fmt.Errorf("%s must be int", label)
	}
	return new(big.Int).Set(intValue.Value), nil
}

func collectionsRangeContains(current, end, step *big.Int, exclusive bool) bool {
	cmp := current.Cmp(end)
	if step.Sign() > 0 {
		if exclusive {
			return cmp < 0
		}
		return cmp <= 0
	}
	if exclusive {
		return cmp > 0
	}
	return cmp >= 0
}

func collectionsIteratorFor(value runtime.Value, label string) (collectionsIterator, error) {
	switch v := value.(type) {
	case *runtime.List:
		index := 0
		return collectionsIterator{next: func() (runtime.Value, bool, error) {
			if index >= len(v.Elements) {
				return nil, false, nil
			}
			next := v.Elements[index]
			index++
			return next, true, nil
		}}, nil
	case *runtime.Generator:
		return collectionsIterator{next: v.Next, close: v.Close}, nil
	case runtime.Range:
		current := new(big.Int).Set(v.Start)
		end := new(big.Int).Set(v.End)
		step := new(big.Int).Set(v.Step)
		return collectionsIterator{next: func() (runtime.Value, bool, error) {
			if !collectionsRangeContains(current, end, step, v.Exclusive) {
				return nil, false, nil
			}
			out := runtime.Int{Value: new(big.Int).Set(current)}
			current.Add(current, step)
			return out, true, nil
		}}, nil
	default:
		return collectionsIterator{}, fmt.Errorf("%s expects iterable", label)
	}
}

func collectionsRange(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects end, optional start/end, or start/end/step", call.Callee.String())
	}
	start := big.NewInt(0)
	end, err := collectionsIntArg(args[0], call.Callee.String()+" end")
	if err != nil {
		return nil, err
	}
	if len(args) >= 2 {
		start, err = collectionsIntArg(args[0], call.Callee.String()+" start")
		if err != nil {
			return nil, err
		}
		end, err = collectionsIntArg(args[1], call.Callee.String()+" end")
		if err != nil {
			return nil, err
		}
	}
	step := big.NewInt(1)
	if len(args) == 3 {
		step, err = collectionsIntArg(args[2], call.Callee.String()+" step")
		if err != nil {
			return nil, err
		}
		if step.Sign() == 0 {
			return nil, fmt.Errorf("%s step cannot be zero", call.Callee.String())
		}
	}
	current := new(big.Int).Set(start)
	return runtime.NewGenerator(func() (runtime.Value, bool, error) {
		if !collectionsRangeContains(current, end, step, true) {
			return nil, false, nil
		}
		out := runtime.Int{Value: new(big.Int).Set(current)}
		current.Add(current, step)
		return out, true, nil
	}), nil
}

func collectionsTake(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects iterable and count", call.Callee.String())
	}
	source, err := collectionsIteratorFor(args[0], call.Callee.String())
	if err != nil {
		return nil, err
	}
	count, err := collectionsIntArg(args[1], call.Callee.String()+" count")
	if err != nil {
		return nil, err
	}
	if count.Sign() < 0 {
		return nil, fmt.Errorf("%s count cannot be negative", call.Callee.String())
	}
	remaining := new(big.Int).Set(count)
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		if remaining.Sign() <= 0 {
			if source.close != nil {
				source.close()
			}
			return nil, false, nil
		}
		next, ok, err := source.next()
		if err != nil || !ok {
			if source.close != nil {
				source.close()
			}
			return next, ok, err
		}
		remaining.Sub(remaining, big.NewInt(1))
		return next, true, nil
	}, func() {
		if source.close != nil {
			source.close()
		}
	}), nil
}

func (e *Evaluator) collectionsLazyMap(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects iterable and function", call.Callee.String())
	}
	source, err := collectionsIteratorFor(args[0], call.Callee.String())
	if err != nil {
		return nil, err
	}
	fn := args[1]
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		next, ok, err := source.next()
		if err != nil || !ok {
			if source.close != nil {
				source.close()
			}
			return next, ok, err
		}
		mapped, err := e.callValue(fn, []runtime.Value{next})
		if err != nil {
			if source.close != nil {
				source.close()
			}
			return nil, false, err
		}
		return mapped, true, nil
	}, func() {
		if source.close != nil {
			source.close()
		}
	}), nil
}

func (e *Evaluator) collectionsLazyFilter(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects iterable and function", call.Callee.String())
	}
	source, err := collectionsIteratorFor(args[0], call.Callee.String())
	if err != nil {
		return nil, err
	}
	fn := args[1]
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		for {
			next, ok, err := source.next()
			if err != nil || !ok {
				if source.close != nil {
					source.close()
				}
				return next, ok, err
			}
			keep, err := e.callValue(fn, []runtime.Value{next})
			if err != nil {
				if source.close != nil {
					source.close()
				}
				return nil, false, err
			}
			if isTruthy(keep) {
				return next, true, nil
			}
		}
	}, func() {
		if source.close != nil {
			source.close()
		}
	}), nil
}

func (e *Evaluator) collectionsMethod(name string, arity int) builtinFunc {
	return func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
		if arity >= 0 && len(args) != arity {
			return nil, fmt.Errorf("%s expects %d argument(s)", call.Callee.String(), arity)
		}
		if name == "sorted" && len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("%s expects list and optional comparison function", call.Callee.String())
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("%s expects a collection", call.Callee.String())
		}
		list, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s expects list", call.Callee.String())
		}
		return e.evalMethodCall(list, name, args[1:])
	}
}

func (e *Evaluator) collectionsGraphMethod(name string, extraArgs int) builtinFunc {
	return func(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1+extraArgs {
			return nil, fmt.Errorf("%s expects %d argument(s)", call.Callee.String(), 1+extraArgs)
		}
		graph, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s expects dict as first argument (adjacency graph)", call.Callee.String())
		}
		return e.evalMethodCall(graph, name, args[1:])
	}
}

func valuesEqualSimple(left runtime.Value, right runtime.Value) bool {
	switch leftValue := left.(type) {
	case *runtime.List:
		rightValue, ok := right.(*runtime.List)
		if !ok || len(leftValue.Elements) != len(rightValue.Elements) {
			return false
		}
		for i, element := range leftValue.Elements {
			if !valuesEqualSimple(element, rightValue.Elements[i]) {
				return false
			}
		}
		return true
	case runtime.Dict:
		rightValue, ok := right.(runtime.Dict)
		if !ok || len(leftValue.Entries) != len(rightValue.Entries) {
			return false
		}
		for key, entry := range leftValue.Entries {
			other, ok := rightValue.Entries[key]
			if !ok || !valuesEqualSimple(entry.Key, other.Key) || !valuesEqualSimple(entry.Value, other.Value) {
				return false
			}
		}
		return true
	case runtime.Set:
		rightValue, ok := right.(runtime.Set)
		if !ok || len(leftValue.Elements) != len(rightValue.Elements) {
			return false
		}
		for key, entry := range leftValue.Elements {
			other, ok := rightValue.Elements[key]
			if !ok || !valuesEqualSimple(entry.Value, other.Value) {
				return false
			}
		}
		return true
	default:
		return primitiveEqual(left, right)
	}
}

func secretsGetEnv(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	name, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	value, ok := os.LookupEnv(name)
	if !ok {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: value}, nil
}

func secretsRequireEnv(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	name, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil, fmt.Errorf("required secret environment variable %s is not set", name)
	}
	return runtime.String{Value: value}, nil
}

func secretsReadFile(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: strings.TrimRight(string(data), "\r\n")}, nil
}

func constantTimeComparableEqual(left []byte, right []byte) bool {
	key := []byte("geblang.secrets.constantTimeEqual.v1")
	leftMAC := hmac.New(sha256.New, key)
	_, _ = leftMAC.Write(left)
	rightMAC := hmac.New(sha256.New, key)
	_, _ = rightMAC.Write(right)
	digestEqual := subtle.ConstantTimeCompare(leftMAC.Sum(nil), rightMAC.Sum(nil))
	lengthEqual := constantTimeIntEqual(len(left), len(right))
	return digestEqual&lengthEqual == 1
}

func constantTimeIntEqual(left int, right int) int {
	diff := uint64(left ^ right)
	diff |= diff >> 32
	diff |= diff >> 16
	diff |= diff >> 8
	diff |= diff >> 4
	diff |= diff >> 2
	diff |= diff >> 1
	return int((diff & 1) ^ 1)
}

func secureRandomBytes(call *ast.CallExpression, args []runtime.Value) ([]byte, error) {
	size, err := singleIntValue(call, args)
	if err != nil {
		return nil, err
	}
	if size < 0 || size > 1<<20 {
		return nil, fmt.Errorf("%s byte count out of range", call.Callee.String())
	}
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return nil, err
	}
	return data, nil
}

func secretComparableBytes(value runtime.Value) ([]byte, error) {
	switch value := value.(type) {
	case runtime.String:
		return []byte(value.Value), nil
	case runtime.Bytes:
		return value.Value, nil
	default:
		return nil, fmt.Errorf("secret comparison expects string or bytes")
	}
}

func schemaValidate(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects value and schema", call.Callee.String())
	}
	schema, ok := args[1].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s schema must be dict", call.Callee.String())
	}
	errors := []runtime.Value{}
	validateValueAgainstSchema(args[0], schema, "$", &errors)
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "valid", runtime.Bool{Value: len(errors) == 0})
	putDict(entries, "errors", &runtime.List{Elements: errors})
	return runtime.Dict{Entries: entries}, nil
}

func validateValueAgainstSchema(value runtime.Value, schema runtime.Dict, path string, errors *[]runtime.Value) {
	if typeName, ok := dictStringField(schema, "type"); ok {
		if !schemaTypeMatches(value, typeName) {
			*errors = append(*errors, runtime.String{Value: path + ": expected " + typeName + ", got " + value.TypeName()})
			return
		}
	}
	if enumValue, ok := dictField(schema, "enum"); ok {
		if values, ok := enumValue.(*runtime.List); ok {
			found := false
			for _, allowed := range values.Elements {
				if valuesEqualSimple(value, allowed) {
					found = true
					break
				}
			}
			if !found {
				*errors = append(*errors, runtime.String{Value: path + ": value is not in enum"})
			}
		}
	}
	if propertiesValue, ok := dictField(schema, "properties"); ok {
		properties, ok := propertiesValue.(runtime.Dict)
		if !ok {
			*errors = append(*errors, runtime.String{Value: path + ": schema.properties must be dict"})
			return
		}
		object, ok := value.(runtime.Dict)
		if !ok {
			*errors = append(*errors, runtime.String{Value: path + ": expected dict for properties"})
			return
		}
		required := schemaRequiredFields(schema)
		for _, entry := range properties.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				*errors = append(*errors, runtime.String{Value: path + ": schema property keys must be strings"})
				continue
			}
			propertySchema, ok := entry.Value.(runtime.Dict)
			if !ok {
				*errors = append(*errors, runtime.String{Value: path + "." + key.Value + ": property schema must be dict"})
				continue
			}
			propertyValue, exists := object.Entries[dictKey(key)]
			if !exists {
				if required[key.Value] {
					*errors = append(*errors, runtime.String{Value: path + "." + key.Value + ": required field is missing"})
				}
				continue
			}
			validateValueAgainstSchema(propertyValue.Value, propertySchema, path+"."+key.Value, errors)
		}
	}
	if itemsValue, ok := dictField(schema, "items"); ok {
		itemSchema, ok := itemsValue.(runtime.Dict)
		if !ok {
			*errors = append(*errors, runtime.String{Value: path + ": schema.items must be dict"})
			return
		}
		list, ok := value.(*runtime.List)
		if !ok {
			*errors = append(*errors, runtime.String{Value: path + ": expected list for items"})
			return
		}
		for i, item := range list.Elements {
			validateValueAgainstSchema(item, itemSchema, fmt.Sprintf("%s[%d]", path, i), errors)
		}
	}
}

func schemaTypeMatches(value runtime.Value, typeName string) bool {
	switch typeName {
	case "number":
		return value.TypeName() == "int" || value.TypeName() == "decimal" || value.TypeName() == "float"
	case "object":
		return value.TypeName() == "dict"
	case "array":
		return value.TypeName() == "list"
	default:
		return value.TypeName() == typeName
	}
}

func schemaRequiredFields(schema runtime.Dict) map[string]bool {
	required := map[string]bool{}
	value, ok := dictField(schema, "required")
	if !ok {
		return required
	}
	list, ok := value.(*runtime.List)
	if !ok {
		return required
	}
	for _, item := range list.Elements {
		if field, ok := item.(runtime.String); ok {
			required[field.Value] = true
		}
	}
	return required
}

func (e *Evaluator) serdeParse(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects format and text", call.Callee.String())
	}
	format, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s format must be string", call.Callee.String())
	}
	text, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s text must be string", call.Callee.String())
	}
	var (
		value    runtime.Value
		parseErr *native.ParseError
	)
	switch strings.ToLower(format.Value) {
	case "json":
		value, parseErr = native.ParseJSONText(text.Value)
	case "toml":
		value, parseErr = native.ParseTOMLText(text.Value)
	case "yaml", "yml":
		value, parseErr = native.ParseYAMLText(text.Value)
	default:
		return nil, fmt.Errorf("%s unsupported format %q", call.Callee.String(), format.Value)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("%s", parseErr.Message)
	}
	return value, nil
}

func (e *Evaluator) serdeStringify(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects format and value", call.Callee.String())
	}
	format, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s format must be string", call.Callee.String())
	}
	var module string
	switch strings.ToLower(format.Value) {
	case "json":
		module = "json"
	case "toml":
		module = "toml"
	case "yaml", "yml":
		module = "yaml"
	default:
		return nil, fmt.Errorf("%s unsupported format %q", call.Callee.String(), format.Value)
	}
	return e.natives.Call(module, "stringify", []runtime.Value{args[1]})
}

func (e *Evaluator) dotenvParse(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	text, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return dotenvParseText(text), nil
}

func (e *Evaluator) dotenvLoad(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	data, ioErr := os.ReadFile(path)
	if ioErr != nil {
		return nil, fmt.Errorf("%s: %v", call.Callee.String(), ioErr)
	}
	return dotenvParseText(string(data)), nil
}

func (e *Evaluator) dotenvApply(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one dict argument", call.Callee.String())
	}
	d, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s argument must be a dict", call.Callee.String())
	}
	for _, entry := range d.Entries {
		k, ok := entry.Key.(runtime.String)
		if !ok {
			continue
		}
		v, ok := entry.Value.(runtime.String)
		if !ok {
			continue
		}
		os.Setenv(k.Value, v.Value)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) dotenvLoadAndApply(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	result, err := e.dotenvLoad(call, args)
	if err != nil {
		return nil, err
	}
	_, err = e.dotenvApply(call, []runtime.Value{result})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func dotenvParseText(text string) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[7:])
		}
		eqIdx := strings.IndexByte(line, '=')
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eqIdx])
		if key == "" {
			continue
		}
		value := dotenvParseValue(line[eqIdx+1:])
		kv := runtime.String{Value: key}
		entries[dictKey(kv)] = runtime.DictEntry{Key: kv, Value: runtime.String{Value: value}}
	}
	return runtime.Dict{Entries: entries}
}

func dotenvParseValue(raw string) string {
	raw = strings.TrimLeft(raw, " \t")
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		end := strings.LastIndexByte(raw, '"')
		if end > 0 {
			inner := raw[1:end]
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\t`, "\t")
			inner = strings.ReplaceAll(inner, `\r`, "\r")
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\\`, `\`)
			return inner
		}
	}
	if raw[0] == '\'' {
		end := strings.LastIndexByte(raw, '\'')
		if end > 0 {
			return raw[1:end]
		}
	}
	if idx := strings.Index(raw, " #"); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.TrimRight(raw, " \t")
}

func (e *Evaluator) cliPrompt(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects prompt and optional default", call.Callee.String())
	}
	prompt, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s prompt must be string", call.Callee.String())
	}
	defaultValue := ""
	if len(args) == 2 {
		value, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s default must be string", call.Callee.String())
		}
		defaultValue = value.Value
	}
	_, _ = io.WriteString(e.stdout, prompt.Value)
	line, err := readConsoleLine()
	if err != nil {
		return nil, err
	}
	if line == "" && len(args) == 2 {
		line = defaultValue
	}
	return runtime.String{Value: line}, nil
}

func (e *Evaluator) cliPassword(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects optional prompt", call.Callee.String())
	}
	prompt := ""
	if len(args) == 1 {
		value, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s prompt must be string", call.Callee.String())
		}
		prompt = value.Value
	}
	_, _ = io.WriteString(e.stdout, prompt)
	line, err := readConsoleSecret()
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintln(e.stdout)
	return runtime.String{Value: line}, nil
}

func (e *Evaluator) cliConfirm(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects prompt and optional default bool", call.Callee.String())
	}
	prompt, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s prompt must be string", call.Callee.String())
	}
	defaultValue := false
	hasDefault := false
	if len(args) == 2 {
		value, ok := args[1].(runtime.Bool)
		if !ok {
			return nil, fmt.Errorf("%s default must be bool", call.Callee.String())
		}
		defaultValue = value.Value
		hasDefault = true
	}
	_, _ = io.WriteString(e.stdout, prompt.Value)
	line, err := readConsoleLine()
	if err != nil {
		return nil, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" && hasDefault {
		return runtime.Bool{Value: defaultValue}, nil
	}
	switch answer {
	case "y", "yes", "true", "1":
		return runtime.Bool{Value: true}, nil
	case "n", "no", "false", "0":
		return runtime.Bool{Value: false}, nil
	default:
		return nil, fmt.Errorf("%s expected yes or no", call.Callee.String())
	}
}

func (e *Evaluator) cliChoose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects prompt, options list, and optional default index", call.Callee.String())
	}
	prompt, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s prompt must be string", call.Callee.String())
	}
	options, ok := args[1].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s options must be list<string>", call.Callee.String())
	}
	choices := make([]string, 0, len(options.Elements))
	for _, option := range options.Elements {
		value, ok := option.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s options must be list<string>", call.Callee.String())
		}
		choices = append(choices, value.Value)
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("%s options must not be empty", call.Callee.String())
	}
	defaultIndex := int64(-1)
	if len(args) == 3 {
		value, ok := args[2].(runtime.Int)
		if !ok || !value.Value.IsInt64() {
			return nil, fmt.Errorf("%s default index must be int", call.Callee.String())
		}
		defaultIndex = value.Value.Int64()
		if defaultIndex < 0 || defaultIndex >= int64(len(choices)) {
			return nil, fmt.Errorf("%s default index out of range", call.Callee.String())
		}
	}
	_, _ = io.WriteString(e.stdout, prompt.Value)
	for i, choice := range choices {
		_, _ = fmt.Fprintf(e.stdout, "\n  %d) %s", i+1, choice)
	}
	_, _ = io.WriteString(e.stdout, "\n> ")
	line, err := readConsoleLine()
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" && defaultIndex >= 0 {
		return runtime.String{Value: choices[defaultIndex]}, nil
	}
	index, err := strconv.ParseInt(line, 10, 64)
	if err == nil && index >= 1 && index <= int64(len(choices)) {
		return runtime.String{Value: choices[index-1]}, nil
	}
	for _, choice := range choices {
		if strings.EqualFold(choice, line) {
			return runtime.String{Value: choice}, nil
		}
	}
	return nil, fmt.Errorf("%s invalid choice %q", call.Callee.String(), line)
}

func readConsoleLine() (string, error) {
	line, err := consoleLineReader().ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if err == io.EOF && line == "" {
		return "", nil
	}
	return line, nil
}

var consoleReaderState struct {
	sync.Mutex
	file   *os.File
	reader *bufio.Reader
}

func consoleLineReader() *bufio.Reader {
	consoleReaderState.Lock()
	defer consoleReaderState.Unlock()
	if consoleReaderState.reader == nil || consoleReaderState.file != os.Stdin {
		consoleReaderState.file = os.Stdin
		consoleReaderState.reader = bufio.NewReader(os.Stdin)
	}
	return consoleReaderState.reader
}

func readConsoleSecret() (string, error) {
	fd := int(os.Stdin.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return readConsoleLine()
	}
	hidden := *original
	hidden.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &hidden); err != nil {
		return readConsoleLine()
	}
	line, readErr := readConsoleLine()
	restoreErr := unix.IoctlSetTermios(fd, unix.TCSETS, original)
	if readErr != nil {
		return "", readErr
	}
	if restoreErr != nil {
		return "", restoreErr
	}
	return line, nil
}

func cliStyle(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects text and style options", call.Callee.String())
	}
	text, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s text must be string", call.Callee.String())
	}
	codes, err := ansiStyleCodes(args[1])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Callee.String(), err)
	}
	if len(codes) == 0 {
		return text, nil
	}
	return runtime.String{Value: "\x1b[" + strings.Join(codes, ";") + "m" + text.Value + "\x1b[0m"}, nil
}

func ansiStyleCodes(value runtime.Value) ([]string, error) {
	switch value := value.(type) {
	case runtime.String:
		return ansiNamedStyle(value.Value)
	case runtime.Dict:
		codes := []string{}
		if truthyDictBool(value, "bold") {
			codes = append(codes, "1")
		}
		if truthyDictBool(value, "dim") {
			codes = append(codes, "2")
		}
		if truthyDictBool(value, "italic") {
			codes = append(codes, "3")
		}
		if truthyDictBool(value, "underline") {
			codes = append(codes, "4")
		}
		if truthyDictBool(value, "inverse") {
			codes = append(codes, "7")
		}
		if fg, ok := dictStringField(value, "fg"); ok {
			code, ok := ansiColorCode(fg, false)
			if !ok {
				return nil, fmt.Errorf("unknown foreground color %q", fg)
			}
			codes = append(codes, code)
		}
		if bg, ok := dictStringField(value, "bg"); ok {
			code, ok := ansiColorCode(bg, true)
			if !ok {
				return nil, fmt.Errorf("unknown background color %q", bg)
			}
			codes = append(codes, code)
		}
		return codes, nil
	default:
		return nil, fmt.Errorf("style options must be string or dict")
	}
}

func ansiNamedStyle(name string) ([]string, error) {
	code, ok := ansiColorCode(name, false)
	if ok {
		return []string{code}, nil
	}
	switch strings.ToLower(name) {
	case "bold":
		return []string{"1"}, nil
	case "dim":
		return []string{"2"}, nil
	case "italic":
		return []string{"3"}, nil
	case "underline":
		return []string{"4"}, nil
	case "inverse":
		return []string{"7"}, nil
	case "reset", "plain", "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown style %q", name)
	}
}

func ansiColorCode(name string, background bool) (string, bool) {
	colors := map[string]int{
		"black": 0, "red": 1, "green": 2, "yellow": 3,
		"blue": 4, "magenta": 5, "cyan": 6, "white": 7,
	}
	index, ok := colors[strings.ToLower(name)]
	if !ok {
		return "", false
	}
	base := 30
	if background {
		base = 40
	}
	return strconv.Itoa(base + index), true
}

func truthyDictBool(dict runtime.Dict, key string) bool {
	value, ok := dictField(dict, key)
	if !ok {
		return false
	}
	boolValue, ok := value.(runtime.Bool)
	return ok && boolValue.Value
}

func cliStripANSI(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	text, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: stripANSI(text)}, nil
}

func stripANSI(text string) string {
	var out strings.Builder
	inEscape := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inEscape {
			if ch >= '@' && ch <= '~' {
				inEscape = false
			}
			continue
		}
		if ch == 0x1b && i+1 < len(text) && text[i+1] == '[' {
			inEscape = true
			i++
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func cliTable(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects rows and optional options", call.Callee.String())
	}
	rows, ok := args[0].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s rows must be list", call.Callee.String())
	}
	columns := []string{}
	headers := []string{}
	separator := "  "
	if len(args) == 2 {
		switch opts := args[1].(type) {
		case *runtime.List:
			/* Backwards-compatible legacy form: bare list of header
			 * strings. Columns are inferred from the row dict keys. */
			for _, header := range opts.Elements {
				value, ok := header.(runtime.String)
				if !ok {
					return nil, fmt.Errorf("%s headers must be list<string>", call.Callee.String())
				}
				headers = append(headers, value.Value)
			}
			columns = append([]string(nil), headers...)
		case runtime.Dict:
			/* Documented form: an options dict with `columns`,
			 * `headers`, `separator`. All keys are optional. */
			if value, ok := dictField(opts, "columns"); ok {
				list, ok := value.(*runtime.List)
				if !ok {
					return nil, fmt.Errorf("%s options.columns must be list<string>", call.Callee.String())
				}
				for _, item := range list.Elements {
					s, ok := item.(runtime.String)
					if !ok {
						return nil, fmt.Errorf("%s options.columns must be list<string>", call.Callee.String())
					}
					columns = append(columns, s.Value)
				}
			}
			if value, ok := dictField(opts, "headers"); ok {
				list, ok := value.(*runtime.List)
				if !ok {
					return nil, fmt.Errorf("%s options.headers must be list<string>", call.Callee.String())
				}
				for _, item := range list.Elements {
					s, ok := item.(runtime.String)
					if !ok {
						return nil, fmt.Errorf("%s options.headers must be list<string>", call.Callee.String())
					}
					headers = append(headers, s.Value)
				}
			}
			if value, ok := dictField(opts, "separator"); ok {
				s, ok := value.(runtime.String)
				if !ok {
					return nil, fmt.Errorf("%s options.separator must be string", call.Callee.String())
				}
				separator = s.Value
			}
		default:
			return nil, fmt.Errorf("%s second argument must be list<string> or options dict", call.Callee.String())
		}
	}
	tableRows, inferred, err := cliTableRows(rows, columns)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Callee.String(), err)
	}
	if len(columns) == 0 {
		columns = inferred
	}
	if len(headers) == 0 {
		headers = columns
	}
	return runtime.String{Value: renderTableWithSeparator(headers, tableRows, separator)}, nil
}

// cliSpinnerFrames are the unicode spinner phases used by
// cli.Spinner. Falls back to ASCII when stderr is not a TTY.
var cliSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// cliSpinnerTick renders one frame of an ANSI spinner to stderr.
// Args: (frameIndex: int, message: string). Returns the next frame
// index. The Geblang stdlib wrapper holds the index field and calls
// this on each .tick().
func (e *Evaluator) cliSpinnerTick(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (frameIndex, message)", call.Callee.String())
	}
	idx, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s frameIndex must be int", call.Callee.String())
	}
	msg, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s message must be string", call.Callee.String())
	}
	frame := cliSpinnerFrames[int(idx)%len(cliSpinnerFrames)]
	_, _ = fmt.Fprintf(e.stderr, "\r%s %s", frame, msg.Value)
	return runtime.NewInt64((idx + 1) % int64(len(cliSpinnerFrames))), nil
}

// cliSpinnerStop clears the spinner line.
func (e *Evaluator) cliSpinnerStop(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects (finalMessage?)", call.Callee.String())
	}
	final := ""
	if len(args) == 1 {
		s, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s finalMessage must be string", call.Callee.String())
		}
		final = s.Value
	}
	_, _ = fmt.Fprint(e.stderr, "\r\x1b[2K")
	if final != "" {
		_, _ = fmt.Fprintln(e.stderr, final)
	}
	return runtime.Null{}, nil
}

// cliProgressRender draws an ANSI progress bar to stderr.
// Args: (current: int, total: int, width: int = 30, label: string = "").
// Renders [#####-----] 50% (5/10) label.
func (e *Evaluator) cliProgressRender(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 4 {
		return nil, fmt.Errorf("%s expects (current, total, width?, label?)", call.Callee.String())
	}
	current, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s current must be int", call.Callee.String())
	}
	total, ok := native.AsInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s total must be int", call.Callee.String())
	}
	width := int64(30)
	if len(args) >= 3 {
		n, ok := native.AsInt64(args[2])
		if !ok {
			return nil, fmt.Errorf("%s width must be int", call.Callee.String())
		}
		width = n
	}
	label := ""
	if len(args) == 4 {
		s, ok := args[3].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s label must be string", call.Callee.String())
		}
		label = s.Value
	}
	if total <= 0 {
		total = 1
	}
	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}
	filled := int(width * current / total)
	pct := int(100 * current / total)
	bar := strings.Repeat("#", filled) + strings.Repeat("-", int(width)-filled)
	line := fmt.Sprintf("\r[%s] %d%% (%d/%d)", bar, pct, current, total)
	if label != "" {
		line += " " + label
	}
	_, _ = fmt.Fprint(e.stderr, line)
	return runtime.Null{}, nil
}

// cliProgressFinish clears the progress line and optionally prints
// a final message.
func (e *Evaluator) cliProgressFinish(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects (finalMessage?)", call.Callee.String())
	}
	final := ""
	if len(args) == 1 {
		s, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s finalMessage must be string", call.Callee.String())
		}
		final = s.Value
	}
	_, _ = fmt.Fprint(e.stderr, "\r\x1b[2K")
	if final != "" {
		_, _ = fmt.Fprintln(e.stderr, final)
	}
	return runtime.Null{}, nil
}

func cliTableRows(rows *runtime.List, headers []string) ([][]string, []string, error) {
	out := [][]string{}
	inferred := append([]string(nil), headers...)
	for _, row := range rows.Elements {
		switch row := row.(type) {
		case runtime.Dict:
			if len(inferred) == 0 {
				for _, entry := range row.Entries {
					if key, ok := entry.Key.(runtime.String); ok {
						inferred = append(inferred, key.Value)
					}
				}
				sort.Strings(inferred)
			}
			values := make([]string, len(inferred))
			for i, header := range inferred {
				if value, ok := dictField(row, header); ok {
					values[i] = value.Inspect()
				}
			}
			out = append(out, values)
		case *runtime.List:
			values := make([]string, len(row.Elements))
			for i, value := range row.Elements {
				values[i] = value.Inspect()
			}
			out = append(out, values)
		default:
			return nil, nil, fmt.Errorf("row must be dict or list, got %s", row.TypeName())
		}
	}
	return out, inferred, nil
}

func renderTable(headers []string, rows [][]string) string {
	return renderTableWithSeparator(headers, rows, "  ")
}

func renderTableWithSeparator(headers []string, rows [][]string, separator string) string {
	widths := []int{}
	if len(headers) > 0 {
		widths = make([]int, len(headers))
		for i, header := range headers {
			widths[i] = len(header)
		}
	}
	for _, row := range rows {
		if len(row) > len(widths) {
			widths = append(widths, make([]int, len(row)-len(widths))...)
		}
		for i, value := range row {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}
	var out strings.Builder
	if len(headers) > 0 {
		writeTableRow(&out, headers, widths, separator)
		dashes := make([]string, len(widths))
		for i, width := range widths {
			dashes[i] = strings.Repeat("-", width)
		}
		writeTableRow(&out, dashes, widths, separator)
	}
	for _, row := range rows {
		writeTableRow(&out, row, widths, separator)
	}
	return strings.TrimRight(out.String(), "\n")
}

func writeTableRow(out *strings.Builder, row []string, widths []int, separator string) {
	for i, width := range widths {
		if i > 0 {
			out.WriteString(separator)
		}
		value := ""
		if i < len(row) {
			value = row[i]
		}
		out.WriteString(value)
		if pad := width - len(value); pad > 0 {
			out.WriteString(strings.Repeat(" ", pad))
		}
	}
	out.WriteByte('\n')
}

func (e *Evaluator) metricsInc(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects name, optional amount, optional labels", call.Callee.String())
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s name must be string", call.Callee.String())
	}
	amount := 1.0
	if len(args) >= 2 {
		if labels, ok := args[1].(runtime.Dict); ok && len(args) == 2 {
			// Two-arg variant with a dict in the second slot: treat it
			// as labels with default amount = 1.
			return e.metricsIncCommit(call, name.Value, 1.0, labels)
		}
		value, err := metricNumber(args[1])
		if err != nil {
			return nil, err
		}
		amount = value
	}
	labels := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	if len(args) == 3 {
		labels, ok = args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s labels must be dict", call.Callee.String())
		}
	}
	return e.metricsIncCommit(call, name.Value, amount, labels)
}

func (e *Evaluator) metricsIncCommit(call *ast.CallExpression, name string, amount float64, labels runtime.Dict) (runtime.Value, error) {
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	if entry, ok := e.metricRegistry[name]; ok {
		if entry.kind != "counter" && entry.kind != "gauge" {
			return nil, fmt.Errorf("metric %q is a %s; use metrics.observe to record samples", name, entry.kind)
		}
		key, _, err := labelKeyFromDict(entry, labels)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", call.Callee.String(), err)
		}
		entry.values[key] += amount
		return runtime.Float{Value: entry.values[key]}, nil
	}
	if len(labels.Entries) > 0 {
		return nil, fmt.Errorf("metric %q is not declared; call metrics.counter(%q, {labels: ...}) first", name, name)
	}
	e.metrics[name] += amount
	return runtime.Float{Value: e.metrics[name]}, nil
}

func (e *Evaluator) metricsSet(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects name, value, optional labels", call.Callee.String())
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s name must be string", call.Callee.String())
	}
	value, err := metricNumber(args[1])
	if err != nil {
		return nil, err
	}
	labels := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	if len(args) == 3 {
		labels, ok = args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s labels must be dict", call.Callee.String())
		}
	}
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	if entry, ok := e.metricRegistry[name.Value]; ok {
		if entry.kind != "gauge" && entry.kind != "counter" {
			return nil, fmt.Errorf("metric %q is a %s; use metrics.observe to record samples", name.Value, entry.kind)
		}
		key, _, err := labelKeyFromDict(entry, labels)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", call.Callee.String(), err)
		}
		entry.values[key] = value
		return runtime.Float{Value: value}, nil
	}
	if len(labels.Entries) > 0 {
		return nil, fmt.Errorf("metric %q is not declared; call metrics.gauge(%q, {labels: ...}) first", name.Value, name.Value)
	}
	e.metrics[name.Value] = value
	return runtime.Float{Value: value}, nil
}

func (e *Evaluator) metricsGet(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	name, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	return runtime.Float{Value: e.metrics[name]}, nil
}

func (e *Evaluator) metricsSnapshot(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	entries := map[string]runtime.DictEntry{}
	for key, value := range e.metrics {
		putDict(entries, key, runtime.Float{Value: value})
	}
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) metricsReset(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 && len(args) != 1 {
		return nil, fmt.Errorf("%s expects optional name", call.Callee.String())
	}
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	if len(args) == 0 {
		e.metrics = map[string]float64{}
		return runtime.Null{}, nil
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s name must be string", call.Callee.String())
	}
	delete(e.metrics, name.Value)
	return runtime.Null{}, nil
}

func metricsNow(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	return runtime.NewInt64(time.Now().UnixNano()), nil
}

func metricsDuration(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects start timestamp", call.Callee.String())
	}
	startNs, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s start timestamp must be int", call.Callee.String())
	}
	return runtime.NewInt64(time.Now().UnixNano() - startNs), nil
}

// metricsEntry is one declared metric in the registry. counter and
// gauge use `values` (one float per label-tuple); histogram uses
// `histStates` (running bucket counts per label-tuple) and `buckets`
// (the configured upper bounds, ascending).
type metricsEntry struct {
	kind       string
	help       string
	labelKeys  []string
	values     map[string]float64
	buckets    []float64
	histStates map[string]*histState
}

type histState struct {
	counts []uint64
	sum    float64
	count  uint64
}

// labelKeyFromDict produces a stable string key for a label-value
// tuple by sorting the entry's labelKeys and joining `name=value`
// pairs with ASCII unit separator. Missing labels are treated as the
// empty string. Extra labels not declared on the metric are rejected.
func labelKeyFromDict(entry *metricsEntry, fields runtime.Dict) (string, []string, error) {
	if len(entry.labelKeys) == 0 {
		if len(fields.Entries) > 0 {
			return "", nil, fmt.Errorf("metric was declared with no labels")
		}
		return "", nil, nil
	}
	declared := map[string]string{}
	for _, k := range entry.labelKeys {
		declared[native.DictKey(runtime.String{Value: k})] = k
	}
	for k := range fields.Entries {
		if _, ok := declared[k]; !ok {
			return "", nil, fmt.Errorf("undeclared label key %q", k)
		}
	}
	values := make([]string, len(entry.labelKeys))
	for i, k := range entry.labelKeys {
		ent, ok := fields.Entries[native.DictKey(runtime.String{Value: k})]
		if !ok {
			values[i] = ""
			continue
		}
		s, ok := ent.Value.(runtime.String)
		if !ok {
			return "", nil, fmt.Errorf("label %q must be string", k)
		}
		values[i] = s.Value
	}
	return strings.Join(values, "\x1f"), values, nil
}

func (e *Evaluator) declareMetric(call *ast.CallExpression, args []runtime.Value, kind string) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects name and optional opts", call.Callee.String())
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s name must be string", call.Callee.String())
	}
	entry := &metricsEntry{
		kind:   kind,
		values: map[string]float64{},
	}
	if kind == "histogram" {
		entry.buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
		entry.histStates = map[string]*histState{}
	}
	if len(args) == 2 {
		opts, ok := args[1].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s opts must be dict", call.Callee.String())
		}
		if helpEnt, ok := opts.Entries[native.DictKey(runtime.String{Value: "help"})]; ok {
			if s, ok := helpEnt.Value.(runtime.String); ok {
				entry.help = s.Value
			}
		}
		if labelEnt, ok := opts.Entries[native.DictKey(runtime.String{Value: "labels"})]; ok {
			list, ok := labelEnt.Value.(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("%s opts.labels must be list of strings", call.Callee.String())
			}
			for _, item := range list.Elements {
				s, ok := item.(runtime.String)
				if !ok {
					return nil, fmt.Errorf("%s opts.labels entries must be strings", call.Callee.String())
				}
				entry.labelKeys = append(entry.labelKeys, s.Value)
			}
		}
		if bucketEnt, ok := opts.Entries[native.DictKey(runtime.String{Value: "buckets"})]; ok {
			if kind != "histogram" {
				return nil, fmt.Errorf("%s opts.buckets is only valid for histograms", call.Callee.String())
			}
			list, ok := bucketEnt.Value.(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("%s opts.buckets must be list of numbers", call.Callee.String())
			}
			entry.buckets = entry.buckets[:0]
			for _, item := range list.Elements {
				v, err := metricNumber(item)
				if err != nil {
					return nil, fmt.Errorf("%s opts.buckets: %v", call.Callee.String(), err)
				}
				entry.buckets = append(entry.buckets, v)
			}
		}
	}
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	if existing, ok := e.metricRegistry[name.Value]; ok && existing.kind != kind {
		return nil, fmt.Errorf("metric %q already declared as %s", name.Value, existing.kind)
	}
	e.metricRegistry[name.Value] = entry
	return runtime.String{Value: name.Value}, nil
}

func (e *Evaluator) metricsCounter(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.declareMetric(call, args, "counter")
}

func (e *Evaluator) metricsGauge(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.declareMetric(call, args, "gauge")
}

func (e *Evaluator) metricsHistogram(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.declareMetric(call, args, "histogram")
}

// metricsObserve records a histogram sample. Signature:
//
//	metrics.observe(name, value)               // no labels
//	metrics.observe(name, value, {labelDict})  // with labels
func (e *Evaluator) metricsObserve(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects name, value, and optional labels", call.Callee.String())
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s name must be string", call.Callee.String())
	}
	value, err := metricNumber(args[1])
	if err != nil {
		return nil, err
	}
	labels := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	if len(args) == 3 {
		labels, ok = args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s labels must be dict", call.Callee.String())
		}
	}
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	entry, ok := e.metricRegistry[name.Value]
	if !ok {
		return nil, fmt.Errorf("metric %q is not declared - call metrics.histogram(%q, ...) first", name.Value, name.Value)
	}
	if entry.kind != "histogram" {
		return nil, fmt.Errorf("metric %q is a %s, not a histogram", name.Value, entry.kind)
	}
	key, _, err := labelKeyFromDict(entry, labels)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", call.Callee.String(), err)
	}
	state, ok := entry.histStates[key]
	if !ok {
		state = &histState{counts: make([]uint64, len(entry.buckets))}
		entry.histStates[key] = state
	}
	for i, upper := range entry.buckets {
		if value <= upper {
			state.counts[i]++
		}
	}
	state.sum += value
	state.count++
	return runtime.Null{}, nil
}

// metricsToPrometheus emits the registered metrics in Prometheus
// text exposition format (v0.0.4). Legacy `metrics.inc/set` entries
// that are not also declared with metrics.counter/gauge appear as
// untyped at the end.
func (e *Evaluator) metricsToPrometheus(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	e.metricsMu.Lock()
	defer e.metricsMu.Unlock()
	var buf strings.Builder
	names := make([]string, 0, len(e.metricRegistry))
	for name := range e.metricRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := e.metricRegistry[name]
		if entry.help != "" {
			fmt.Fprintf(&buf, "# HELP %s %s\n", name, escapePromHelp(entry.help))
		}
		fmt.Fprintf(&buf, "# TYPE %s %s\n", name, entry.kind)
		switch entry.kind {
		case "counter", "gauge":
			writeLabeledValues(&buf, name, entry)
		case "histogram":
			writeHistogram(&buf, name, entry)
		}
	}
	// Legacy flat entries that weren't promoted via counter/gauge.
	legacyNames := make([]string, 0, len(e.metrics))
	for name := range e.metrics {
		if _, declared := e.metricRegistry[name]; declared {
			continue
		}
		legacyNames = append(legacyNames, name)
	}
	sort.Strings(legacyNames)
	for _, name := range legacyNames {
		fmt.Fprintf(&buf, "# TYPE %s untyped\n", name)
		fmt.Fprintf(&buf, "%s %s\n", name, formatPromFloat(e.metrics[name]))
	}
	return runtime.String{Value: buf.String()}, nil
}

func writeLabeledValues(buf *strings.Builder, name string, entry *metricsEntry) {
	keys := make([]string, 0, len(entry.values))
	for k := range entry.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(buf, "%s%s %s\n", name, promLabelsFromKey(entry.labelKeys, k), formatPromFloat(entry.values[k]))
	}
	if len(keys) == 0 && len(entry.labelKeys) == 0 {
		fmt.Fprintf(buf, "%s 0\n", name)
	}
}

func writeHistogram(buf *strings.Builder, name string, entry *metricsEntry) {
	keys := make([]string, 0, len(entry.histStates))
	for k := range entry.histStates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		keys = []string{""}
	}
	for _, k := range keys {
		state, ok := entry.histStates[k]
		if !ok {
			state = &histState{counts: make([]uint64, len(entry.buckets))}
		}
		baseLabels := promLabelsFromKey(entry.labelKeys, k)
		for i, upper := range entry.buckets {
			labelList := promLabelsAppendLE(baseLabels, formatPromFloat(upper))
			fmt.Fprintf(buf, "%s_bucket%s %d\n", name, labelList, state.counts[i])
		}
		labelList := promLabelsAppendLE(baseLabels, "+Inf")
		fmt.Fprintf(buf, "%s_bucket%s %d\n", name, labelList, state.count)
		fmt.Fprintf(buf, "%s_sum%s %s\n", name, baseLabels, formatPromFloat(state.sum))
		fmt.Fprintf(buf, "%s_count%s %d\n", name, baseLabels, state.count)
	}
}

func promLabelsFromKey(labelNames []string, key string) string {
	if len(labelNames) == 0 {
		return ""
	}
	values := strings.Split(key, "\x1f")
	if len(values) != len(labelNames) {
		return ""
	}
	pairs := make([]string, 0, len(labelNames))
	for i, name := range labelNames {
		pairs = append(pairs, fmt.Sprintf("%s=%q", name, escapePromLabel(values[i])))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func promLabelsAppendLE(base, upper string) string {
	if base == "" {
		return fmt.Sprintf("{le=%q}", upper)
	}
	return base[:len(base)-1] + fmt.Sprintf(",le=%q}", upper)
}

func escapePromLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func escapePromHelp(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func formatPromFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func metricNumber(value runtime.Value) (float64, error) {
	switch value := value.(type) {
	case runtime.SmallInt:
		return float64(value.Value), nil
	case runtime.Int:
		result, _ := new(big.Rat).SetInt(value.Value).Float64()
		return result, nil
	case runtime.Decimal:
		result, _ := value.Value.Float64()
		return result, nil
	case runtime.Float:
		return value.Value, nil
	default:
		return 0, fmt.Errorf("metric value must be numeric")
	}
}

func (e *Evaluator) traceStart(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects name, optional attrs, optional opts", call.Callee.String())
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s name must be string", call.Callee.String())
	}
	attrs, err := optionalAttrs(call, args, 1)
	if err != nil {
		return nil, err
	}
	// Optional third arg: opts dict carrying `parent` (a span handle
	// previously returned by trace.start). When present, the new span
	// inherits the parent's traceID and records its parent's spanID.
	var parent *traceSpan
	if len(args) == 3 {
		opts, ok := args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s opts must be dict", call.Callee.String())
		}
		if parentEnt, ok := opts.Entries[native.DictKey(runtime.String{Value: "parent"})]; ok {
			parentID, err := traceHandleID(parentEnt.Value)
			if err != nil {
				return nil, fmt.Errorf("%s opts.parent: %v", call.Callee.String(), err)
			}
			e.traceMu.Lock()
			parent = e.traces[parentID]
			e.traceMu.Unlock()
			if parent == nil {
				return nil, fmt.Errorf("%s opts.parent: unknown span %d", call.Callee.String(), parentID)
			}
		}
	}
	span := &traceSpan{
		name:   name.Value,
		start:  time.Now(),
		attrs:  attrs,
		events: []traceEvent{},
		spanID: randomBytes(8),
	}
	if parent != nil && len(parent.traceID) > 0 {
		span.traceID = parent.traceID
		span.parentSpanID = parent.spanID
	} else {
		span.traceID = randomBytes(16)
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	e.nextTraceID++
	e.traces[e.nextTraceID] = span
	return runtime.NewInt64(e.nextTraceID), nil
}

func randomBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// Fall back to a low-quality timestamp seed - never blocks the
		// VM on entropy starvation. Tracing IDs are not security-
		// sensitive; collision risk is negligible for typical loads.
		for i := range buf {
			buf[i] = byte(time.Now().UnixNano() >> uint(i*8))
		}
	}
	return buf
}

func (e *Evaluator) traceEvent(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects span, name, and optional attrs", call.Callee.String())
	}
	id, err := traceHandleID(args[0])
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s event name must be string", call.Callee.String())
	}
	attrs, err := optionalAttrs(call, args, 2)
	if err != nil {
		return nil, err
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	span, ok := e.traces[id]
	if !ok {
		return nil, fmt.Errorf("unknown trace span %d", id)
	}
	span.events = append(span.events, traceEvent{name: name.Value, at: time.Now(), attrs: attrs})
	return runtime.Null{}, nil
}

func (e *Evaluator) traceEnd(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	id, err := traceHandleID(args[0])
	if err != nil {
		return nil, err
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	span, ok := e.traces[id]
	if !ok {
		return nil, fmt.Errorf("unknown trace span %d", id)
	}
	if !span.ended {
		span.end = time.Now()
		span.ended = true
	}
	return traceSpanValue(id, span), nil
}

func (e *Evaluator) traceSnapshot(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	spans := make([]runtime.Value, 0, len(e.traces))
	for id, span := range e.traces {
		spans = append(spans, traceSpanValue(id, span))
	}
	return &runtime.List{Elements: spans}, nil
}

func (e *Evaluator) traceReset(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	e.traces = map[int64]*traceSpan{}
	return runtime.Null{}, nil
}

// traceToOtlpJson serializes every recorded span to an OTLP/HTTP
// JSON request body. Signature:
//
//	trace.toOtlpJson()          -> string  (uses default service name)
//	trace.toOtlpJson(opts)      -> string  (opts.serviceName, opts.scopeName, opts.scopeVersion, opts.resource)
//
// Spans without an OTLP traceID/spanID (created before OTLP support
// was added or imported from an older session) are assigned fresh
// IDs at serialization time so the output is always well-formed.
func (e *Evaluator) traceToOtlpJson(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects optional opts dict", call.Callee.String())
	}
	opts := otlpExportOpts{
		serviceName:  "geblang",
		scopeName:    "geblang.trace",
		scopeVersion: version.Geblang,
	}
	if len(args) == 1 {
		if err := opts.applyDict(call, args[0]); err != nil {
			return nil, err
		}
	}
	body, err := e.buildOtlpJson(opts)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: string(body)}, nil
}

// traceExportOtlp POSTs every recorded span to an OTLP/HTTP
// collector. Signature:
//
//	trace.exportOtlp(endpoint)         -> dict {status, ok}
//	trace.exportOtlp(endpoint, opts)   -> dict {status, ok}
//
// endpoint is the collector base URL (e.g. http://localhost:4318);
// `/v1/traces` is appended automatically. opts may set serviceName,
// scopeName, scopeVersion, resource (dict<string, string>), headers
// (dict<string, string>), and timeoutMs.
func (e *Evaluator) traceExportOtlp(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects endpoint and optional opts", call.Callee.String())
	}
	endpoint, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s endpoint must be string", call.Callee.String())
	}
	opts := otlpExportOpts{
		serviceName:  "geblang",
		scopeName:    "geblang.trace",
		scopeVersion: version.Geblang,
		timeoutMs:    10000,
	}
	if len(args) == 2 {
		if err := opts.applyDict(call, args[1]); err != nil {
			return nil, err
		}
	}
	body, err := e.buildOtlpJson(opts)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(endpoint.Value, "/") + "/v1/traces"
	req, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range opts.headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: time.Duration(opts.timeoutMs) * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "status", runtime.NewInt64(int64(resp.StatusCode)))
	putDict(entries, "ok", runtime.Bool{Value: resp.StatusCode >= 200 && resp.StatusCode < 300})
	return runtime.Dict{Entries: entries}, nil
}

type otlpExportOpts struct {
	serviceName  string
	scopeName    string
	scopeVersion string
	resource     map[string]string
	headers      map[string]string
	timeoutMs    int
}

func (o *otlpExportOpts) applyDict(call *ast.CallExpression, value runtime.Value) error {
	dict, ok := value.(runtime.Dict)
	if !ok {
		return fmt.Errorf("%s opts must be dict", call.Callee.String())
	}
	if ent, ok := dict.Entries[native.DictKey(runtime.String{Value: "serviceName"})]; ok {
		s, ok := ent.Value.(runtime.String)
		if !ok {
			return fmt.Errorf("%s opts.serviceName must be string", call.Callee.String())
		}
		o.serviceName = s.Value
	}
	if ent, ok := dict.Entries[native.DictKey(runtime.String{Value: "scopeName"})]; ok {
		s, ok := ent.Value.(runtime.String)
		if !ok {
			return fmt.Errorf("%s opts.scopeName must be string", call.Callee.String())
		}
		o.scopeName = s.Value
	}
	if ent, ok := dict.Entries[native.DictKey(runtime.String{Value: "scopeVersion"})]; ok {
		s, ok := ent.Value.(runtime.String)
		if !ok {
			return fmt.Errorf("%s opts.scopeVersion must be string", call.Callee.String())
		}
		o.scopeVersion = s.Value
	}
	if ent, ok := dict.Entries[native.DictKey(runtime.String{Value: "resource"})]; ok {
		res, ok := ent.Value.(runtime.Dict)
		if !ok {
			return fmt.Errorf("%s opts.resource must be dict<string, string>", call.Callee.String())
		}
		o.resource = stringDictFromRuntime(res)
	}
	if ent, ok := dict.Entries[native.DictKey(runtime.String{Value: "headers"})]; ok {
		hd, ok := ent.Value.(runtime.Dict)
		if !ok {
			return fmt.Errorf("%s opts.headers must be dict<string, string>", call.Callee.String())
		}
		o.headers = stringDictFromRuntime(hd)
	}
	if ent, ok := dict.Entries[native.DictKey(runtime.String{Value: "timeoutMs"})]; ok {
		n, ok := native.AsInt64(ent.Value)
		if !ok {
			return fmt.Errorf("%s opts.timeoutMs must be int", call.Callee.String())
		}
		o.timeoutMs = int(n)
	}
	return nil
}

func stringDictFromRuntime(d runtime.Dict) map[string]string {
	out := map[string]string{}
	for _, ent := range d.Entries {
		k, ok := ent.Key.(runtime.String)
		if !ok {
			continue
		}
		v, ok := ent.Value.(runtime.String)
		if !ok {
			continue
		}
		out[k.Value] = v.Value
	}
	return out
}

// buildOtlpJson constructs an OTLP/HTTP request body (ExportTraceServiceRequest)
// containing every span currently held by the evaluator.
func (e *Evaluator) buildOtlpJson(opts otlpExportOpts) ([]byte, error) {
	e.traceMu.Lock()
	defer e.traceMu.Unlock()

	// Stable ordering by handle so output is deterministic.
	ids := make([]int64, 0, len(e.traces))
	for id := range e.traces {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	spansJSON := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		span := e.traces[id]
		if len(span.traceID) == 0 {
			// Legacy span - assign IDs at serialization time so the
			// output is well-formed for the collector.
			span.traceID = randomBytes(16)
			span.spanID = randomBytes(8)
		}
		endNano := span.start.UnixNano()
		if span.ended {
			endNano = span.end.UnixNano()
		} else {
			endNano = time.Now().UnixNano()
		}
		spanObj := map[string]any{
			"traceId":           hex.EncodeToString(span.traceID),
			"spanId":            hex.EncodeToString(span.spanID),
			"name":              span.name,
			"kind":              1, // SPAN_KIND_INTERNAL
			"startTimeUnixNano": strconv.FormatInt(span.start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(endNano, 10),
		}
		if len(span.parentSpanID) > 0 {
			spanObj["parentSpanId"] = hex.EncodeToString(span.parentSpanID)
		}
		if len(span.attrs) > 0 {
			spanObj["attributes"] = otlpAttributesFromMap(span.attrs)
		}
		if len(span.events) > 0 {
			events := make([]map[string]any, 0, len(span.events))
			for _, ev := range span.events {
				eventObj := map[string]any{
					"name":         ev.name,
					"timeUnixNano": strconv.FormatInt(ev.at.UnixNano(), 10),
				}
				if len(ev.attrs) > 0 {
					eventObj["attributes"] = otlpAttributesFromMap(ev.attrs)
				}
				events = append(events, eventObj)
			}
			spanObj["events"] = events
		}
		spansJSON = append(spansJSON, spanObj)
	}

	resourceAttrs := []map[string]any{
		{"key": "service.name", "value": map[string]any{"stringValue": opts.serviceName}},
	}
	for k, v := range opts.resource {
		if k == "service.name" {
			continue
		}
		resourceAttrs = append(resourceAttrs, map[string]any{
			"key":   k,
			"value": map[string]any{"stringValue": v},
		})
	}

	payload := map[string]any{
		"resourceSpans": []map[string]any{
			{
				"resource": map[string]any{"attributes": resourceAttrs},
				"scopeSpans": []map[string]any{
					{
						"scope": map[string]any{
							"name":    opts.scopeName,
							"version": opts.scopeVersion,
						},
						"spans": spansJSON,
					},
				},
			},
		},
	}
	return json.Marshal(payload)
}

func otlpAttributesFromMap(attrs map[string]runtime.Value) []map[string]any {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{"key": k, "value": otlpAnyValue(attrs[k])})
	}
	return out
}

func otlpAnyValue(value runtime.Value) map[string]any {
	switch v := value.(type) {
	case runtime.String:
		return map[string]any{"stringValue": v.Value}
	case runtime.Bool:
		return map[string]any{"boolValue": v.Value}
	case runtime.SmallInt:
		return map[string]any{"intValue": strconv.FormatInt(v.Value, 10)}
	case runtime.Int:
		if v.Value.IsInt64() {
			return map[string]any{"intValue": strconv.FormatInt(v.Value.Int64(), 10)}
		}
		return map[string]any{"stringValue": v.Value.String()}
	case runtime.Float:
		return map[string]any{"doubleValue": v.Value}
	}
	// Fallback: encode as a JSON string so any attribute value survives.
	if data, err := json.Marshal(value); err == nil {
		return map[string]any{"stringValue": string(data)}
	}
	return map[string]any{"stringValue": fmt.Sprintf("%v", value)}
}

func optionalAttrs(call *ast.CallExpression, args []runtime.Value, index int) (map[string]runtime.Value, error) {
	attrs := map[string]runtime.Value{}
	if len(args) <= index {
		return attrs, nil
	}
	dict, ok := args[index].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s attrs must be dict", call.Callee.String())
	}
	for _, entry := range dict.Entries {
		key, ok := entry.Key.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s attrs keys must be strings", call.Callee.String())
		}
		attrs[key.Value] = entry.Value
	}
	return attrs, nil
}

func traceHandleID(value runtime.Value) (int64, error) {
	id, ok := value.(runtime.Int)
	if !ok || !id.Value.IsInt64() {
		return 0, fmt.Errorf("trace span handle must be int")
	}
	return id.Value.Int64(), nil
}

func traceSpanValue(id int64, span *traceSpan) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "id", runtime.NewInt64(id))
	putDict(entries, "name", runtime.String{Value: span.name})
	putDict(entries, "startUnixNano", runtime.NewInt64(span.start.UnixNano()))
	putDict(entries, "ended", runtime.Bool{Value: span.ended})
	if span.ended {
		putDict(entries, "endUnixNano", runtime.NewInt64(span.end.UnixNano()))
		putDict(entries, "durationNanos", runtime.NewInt64(span.end.Sub(span.start).Nanoseconds()))
	} else {
		putDict(entries, "endUnixNano", runtime.NewInt64(0))
		putDict(entries, "durationNanos", runtime.NewInt64(time.Since(span.start).Nanoseconds()))
	}
	putDict(entries, "attrs", traceAttrsValue(span.attrs))
	events := make([]runtime.Value, 0, len(span.events))
	for _, event := range span.events {
		events = append(events, traceEventValue(event))
	}
	putDict(entries, "events", &runtime.List{Elements: events})
	return runtime.Dict{Entries: entries}
}

func traceEventValue(event traceEvent) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "name", runtime.String{Value: event.name})
	putDict(entries, "unixNano", runtime.NewInt64(event.at.UnixNano()))
	putDict(entries, "attrs", traceAttrsValue(event.attrs))
	return runtime.Dict{Entries: entries}
}

func traceAttrsValue(attrs map[string]runtime.Value) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	for key, value := range attrs {
		putDict(entries, key, value)
	}
	return runtime.Dict{Entries: entries}
}

func profileMemStats(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "alloc", runtime.NewInt64(int64(stats.Alloc)))
	putDict(entries, "totalAlloc", runtime.NewInt64(int64(stats.TotalAlloc)))
	putDict(entries, "sys", runtime.NewInt64(int64(stats.Sys)))
	putDict(entries, "heapAlloc", runtime.NewInt64(int64(stats.HeapAlloc)))
	putDict(entries, "heapSys", runtime.NewInt64(int64(stats.HeapSys)))
	putDict(entries, "heapObjects", runtime.NewInt64(int64(stats.HeapObjects)))
	putDict(entries, "numGC", runtime.NewInt64(int64(stats.NumGC)))
	putDict(entries, "goroutines", runtime.NewInt64(int64(goruntime.NumGoroutine())))
	return runtime.Dict{Entries: entries}, nil
}

func profileGC(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	goruntime.GC()
	return runtime.Null{}, nil
}

func (e *Evaluator) webNew(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	e.webMu.Lock()
	defer e.webMu.Unlock()
	e.nextWebID++
	e.webApps[e.nextWebID] = &webApp{routes: []webRoute{}, beforeMiddlewares: []runtime.Value{}, middlewares: []runtime.Value{}}
	return runtime.NewInt64(e.nextWebID), nil
}

func (e *Evaluator) webUse(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects app and middleware", call.Callee.String())
	}
	app, err := e.webApp(args[0])
	if err != nil {
		return nil, err
	}
	if !runtime.IsCallableValue(args[1]) {
		return nil, fmt.Errorf("%s middleware must be function", call.Callee.String())
	}
	e.webMu.Lock()
	defer e.webMu.Unlock()
	app.middlewares = append(app.middlewares, args[1])
	return runtime.Null{}, nil
}

func (e *Evaluator) webBefore(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects app and middleware", call.Callee.String())
	}
	app, err := e.webApp(args[0])
	if err != nil {
		return nil, err
	}
	if !runtime.IsCallableValue(args[1]) {
		return nil, fmt.Errorf("%s middleware must be function", call.Callee.String())
	}
	e.webMu.Lock()
	defer e.webMu.Unlock()
	app.beforeMiddlewares = append(app.beforeMiddlewares, args[1])
	return runtime.Null{}, nil
}

func (e *Evaluator) webGet(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.webRouteWithMethod(call, args, "GET")
}

func (e *Evaluator) webPost(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.webRouteWithMethod(call, args, "POST")
}

func (e *Evaluator) webRoute(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("%s expects app, method, path, handler", call.Callee.String())
	}
	method, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s method must be string", call.Callee.String())
	}
	return e.registerWebRoute(call, args[0], method.Value, args[2], args[3])
}

func (e *Evaluator) webRouteWithMethod(call *ast.CallExpression, args []runtime.Value, method string) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects app, path, handler", call.Callee.String())
	}
	return e.registerWebRoute(call, args[0], method, args[1], args[2])
}

func (e *Evaluator) registerWebRoute(call *ast.CallExpression, appValue runtime.Value, method string, pathValue runtime.Value, handlerValue runtime.Value) (runtime.Value, error) {
	app, err := e.webApp(appValue)
	if err != nil {
		return nil, err
	}
	path, ok := pathValue.(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	if !runtime.IsCallableValue(handlerValue) {
		return nil, fmt.Errorf("%s handler must be function", call.Callee.String())
	}
	e.webMu.Lock()
	defer e.webMu.Unlock()
	app.routes = append(app.routes, webRoute{method: strings.ToUpper(method), path: path.Value, handler: handlerValue})
	return runtime.Null{}, nil
}

func (e *Evaluator) webHandle(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects app and request", call.Callee.String())
	}
	app, err := e.webApp(args[0])
	if err != nil {
		return nil, err
	}
	request, ok := args[1].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s request must be dict", call.Callee.String())
	}
	e.webMu.Lock()
	routes := append([]webRoute(nil), app.routes...)
	beforeMiddlewares := append([]runtime.Value(nil), app.beforeMiddlewares...)
	middlewares := append([]runtime.Value(nil), app.middlewares...)
	e.webMu.Unlock()
	for _, middleware := range beforeMiddlewares {
		result, err := e.callValue(middleware, []runtime.Value{request})
		if err != nil {
			return nil, err
		}
		if _, ok := result.(runtime.Null); !ok {
			response := normalizeWebResponse(result)
			for i := len(middlewares) - 1; i >= 0; i-- {
				response, err = e.callValue(middlewares[i], []runtime.Value{request, response})
				if err != nil {
					return nil, err
				}
			}
			return response, nil
		}
	}
	method, _ := dictStringField(request, "method")
	path, _ := dictStringField(request, "path")
	for _, route := range routes {
		params, ok := matchWebRoute(route, method, path)
		if !ok {
			continue
		}
		requestWithParams := copyDict(request)
		putDict(requestWithParams.Entries, "params", params)
		response, err := e.callValue(route.handler, []runtime.Value{requestWithParams})
		if err != nil {
			return nil, err
		}
		response = normalizeWebResponse(response)
		for i := len(middlewares) - 1; i >= 0; i-- {
			response, err = e.callValue(middlewares[i], []runtime.Value{requestWithParams, response})
			if err != nil {
				return nil, err
			}
		}
		return response, nil
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "status", runtime.NewInt64(404))
	putDict(entries, "body", runtime.String{Value: "not found"})
	return runtime.Dict{Entries: entries}, nil
}

func normalizeWebResponse(response runtime.Value) runtime.Value {
	if _, ok := response.(runtime.Dict); ok {
		return response
	}
	if _, ok := response.(runtime.Null); ok {
		entries := map[string]runtime.DictEntry{}
		putDict(entries, "status", runtime.NewInt64(204))
		putDict(entries, "body", runtime.String{Value: ""})
		return runtime.Dict{Entries: entries}
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "status", runtime.NewInt64(200))
	putDict(entries, "body", runtime.String{Value: response.Inspect()})
	return runtime.Dict{Entries: entries}
}

func webWithHeader(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects response, name, and value", call.Callee.String())
	}
	response, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s response must be dict", call.Callee.String())
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s header name must be string", call.Callee.String())
	}
	value, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s header value must be string", call.Callee.String())
	}
	out := copyDict(response)
	headersValue, ok := dictField(out, "headers")
	headers := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	if ok {
		existing, ok := headersValue.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s response.headers must be dict", call.Callee.String())
		}
		headers = copyDict(existing)
	}
	putDict(headers.Entries, name.Value, value)
	putDict(out.Entries, "headers", headers)
	return out, nil
}

// webParseMultipart parses a `multipart/form-data` request body and
// returns a dict of the form
//
//	{"fields": dict<string, string>, "files": dict<string, dict>}
//
// where each file entry is `{filename, contentType, bytes}`. The
// argument is the same request dict the framework dispatches to
// route handlers: it must carry a `body` (string or bytes) and a
// `headers` dict with `Content-Type: multipart/form-data; boundary=...`.
//
// Returns an error if the body isn't multipart or the boundary is
// missing/malformed; callers can wrap that as a 400.
func webParseMultipart(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects request dict", call.Callee.String())
	}
	request, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s request must be dict", call.Callee.String())
	}

	headersValue, _ := dictField(request, "headers")
	contentType := ""
	if headers, ok := headersValue.(runtime.Dict); ok {
		for _, entry := range headers.Entries {
			if key, ok := entry.Key.(runtime.String); ok && strings.EqualFold(key.Value, "Content-Type") {
				if v, ok := entry.Value.(runtime.String); ok {
					contentType = v.Value
					break
				}
			}
		}
	}
	if contentType == "" {
		return nil, fmt.Errorf("%s request has no Content-Type header", call.Callee.String())
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("%s parse Content-Type: %v", call.Callee.String(), err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return nil, fmt.Errorf("%s Content-Type is not multipart (got %q)", call.Callee.String(), mediaType)
	}
	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		return nil, fmt.Errorf("%s Content-Type missing boundary", call.Callee.String())
	}

	bodyValue, _ := dictField(request, "body")
	var bodyReader io.Reader
	switch v := bodyValue.(type) {
	case runtime.String:
		bodyReader = strings.NewReader(v.Value)
	case runtime.Bytes:
		bodyReader = bytes.NewReader(v.Value)
	case nil, runtime.Null:
		bodyReader = strings.NewReader("")
	default:
		return nil, fmt.Errorf("%s request body must be string or bytes (got %s)", call.Callee.String(), bodyValue.TypeName())
	}

	reader := multipart.NewReader(bodyReader, boundary)
	fieldsEntries := map[string]runtime.DictEntry{}
	filesEntries := map[string]runtime.DictEntry{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s read part: %v", call.Callee.String(), err)
		}
		name := part.FormName()
		filename := part.FileName()
		data, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			return nil, fmt.Errorf("%s read part body: %v", call.Callee.String(), err)
		}
		if filename == "" {
			putDict(fieldsEntries, name, runtime.String{Value: string(data)})
			continue
		}
		partContentType := part.Header.Get("Content-Type")
		if partContentType == "" {
			partContentType = "application/octet-stream"
		}
		fileEntries := map[string]runtime.DictEntry{}
		putDict(fileEntries, "filename", runtime.String{Value: filename})
		putDict(fileEntries, "contentType", runtime.String{Value: partContentType})
		putDict(fileEntries, "bytes", runtime.Bytes{Value: data})
		putDict(filesEntries, name, runtime.Dict{Entries: fileEntries})
	}

	out := map[string]runtime.DictEntry{}
	putDict(out, "fields", runtime.Dict{Entries: fieldsEntries})
	putDict(out, "files", runtime.Dict{Entries: filesEntries})
	return runtime.Dict{Entries: out}, nil
}

func (e *Evaluator) webApp(value runtime.Value) (*webApp, error) {
	id, ok := value.(runtime.Int)
	if !ok || !id.Value.IsInt64() {
		return nil, fmt.Errorf("web app handle must be int")
	}
	e.webMu.Lock()
	defer e.webMu.Unlock()
	app, ok := e.webApps[id.Value.Int64()]
	if !ok {
		return nil, fmt.Errorf("unknown web app handle %d", id.Value.Int64())
	}
	return app, nil
}

func matchWebRoute(route webRoute, method string, path string) (runtime.Dict, bool) {
	if route.method != strings.ToUpper(method) {
		return runtime.Dict{}, false
	}
	routeParts := splitWebPath(route.path)
	pathParts := splitWebPath(path)
	if len(routeParts) != len(pathParts) {
		return runtime.Dict{}, false
	}
	params := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	for i, routePart := range routeParts {
		if strings.HasPrefix(routePart, ":") && len(routePart) > 1 {
			putDict(params.Entries, routePart[1:], runtime.String{Value: pathParts[i]})
			continue
		}
		if routePart != pathParts[i] {
			return runtime.Dict{}, false
		}
	}
	return params, true
}

func splitWebPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return []string{}
	}
	return strings.Split(path, "/")
}

func copyDict(value runtime.Dict) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	for key, entry := range value.Entries {
		entries[key] = entry
	}
	return runtime.Dict{Entries: entries}
}

func stringWithOptionalUTF8Encoding(call *ast.CallExpression, args []runtime.Value) (string, error) {
	if len(args) != 1 && len(args) != 2 {
		return "", fmt.Errorf("%s expects string and optional encoding", call.Callee.String())
	}
	text, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s text must be string", call.Callee.String())
	}
	if len(args) == 2 {
		encoding, ok := args[1].(runtime.String)
		if !ok {
			return "", fmt.Errorf("%s encoding must be string", call.Callee.String())
		}
		if !isUTF8Encoding(encoding.Value) {
			return "", fmt.Errorf("%s currently supports only utf-8 encoding", call.Callee.String())
		}
	}
	return text.Value, nil
}

func bytesWithOptionalUTF8Encoding(call *ast.CallExpression, args []runtime.Value) ([]byte, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects bytes and optional encoding", call.Callee.String())
	}
	data, ok := args[0].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s data must be bytes", call.Callee.String())
	}
	if len(args) == 2 {
		encoding, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s encoding must be string", call.Callee.String())
		}
		if !isUTF8Encoding(encoding.Value) {
			return nil, fmt.Errorf("%s currently supports only utf-8 encoding", call.Callee.String())
		}
	}
	return data.Value, nil
}

func isUTF8Encoding(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	return normalized == "utf-8" || normalized == "utf8"
}

func singleBytesValue(call *ast.CallExpression, args []runtime.Value) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	value, ok := args[0].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s expects bytes", call.Callee.String())
	}
	return value.Value, nil
}

func pathJoin(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		value, ok := arg.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s arguments must be strings", call.Callee.String())
		}
		parts = append(parts, value.Value)
	}
	return runtime.String{Value: filepath.Join(parts...)}, nil
}

func pathClean(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	value, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: filepath.Clean(value)}, nil
}

func pathBase(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	value, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: filepath.Base(value)}, nil
}

func pathDir(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	value, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: filepath.Dir(value)}, nil
}

func pathExt(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	value, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: filepath.Ext(value)}, nil
}

func pathAbs(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	value, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: abs}, nil
}

func pathRel(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	base, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s base must be string", call.Callee.String())
	}
	target, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s target must be string", call.Callee.String())
	}
	rel, err := filepath.Rel(base.Value, target.Value)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: rel}, nil
}

func pathGlob(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	pattern, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	matches, err := globRecursive(pattern)
	if err != nil {
		return nil, err
	}
	values := make([]runtime.Value, len(matches))
	for i, m := range matches {
		values[i] = runtime.String{Value: m}
	}
	return &runtime.List{Elements: values}, nil
}

// globRecursive extends filepath.Glob with Python-style `**` to match
// zero or more path segments. Paths containing `**` are split into
// prefix / suffix; each candidate under the prefix that satisfies the
// reduced pattern is included.
func globRecursive(pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		return filepath.Glob(pattern)
	}
	// Find the longest static prefix (no glob meta) up to the first **.
	idx := strings.Index(pattern, "**")
	prefix := pattern[:idx]
	suffix := strings.TrimPrefix(pattern[idx+2:], "/")
	root := prefix
	if root == "" {
		root = "."
	} else if strings.HasSuffix(root, "/") {
		root = root[:len(root)-1]
	} else {
		// pattern like "a**b" - anchor the walk at the parent dir of
		// `prefix` and treat the rest as a per-name suffix.
		parent := filepath.Dir(root)
		if parent == "" {
			parent = "."
		}
		root = parent
	}
	var matches []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel := p
		if prefix != "" {
			if !strings.HasPrefix(p, strings.TrimSuffix(prefix, "/")) {
				return nil
			}
			rel = strings.TrimPrefix(p, strings.TrimSuffix(prefix, "/"))
			rel = strings.TrimPrefix(rel, "/")
		}
		if suffix == "" {
			matches = append(matches, p)
			return nil
		}
		ok, err := filepath.Match(suffix, filepath.Base(rel))
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func watchSnapshot(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return watchSnapshotValue(path), nil
}

func watchWait(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects path, optional timeoutMs, optional intervalMs", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	timeout := int64(30000)
	interval := int64(250)
	if len(args) >= 2 {
		n, ok := native.AsInt64(args[1])
		if !ok {
			return nil, fmt.Errorf("%s timeoutMs must be int", call.Callee.String())
		}
		timeout = n
	}
	if len(args) == 3 {
		n, ok := native.AsInt64(args[2])
		if !ok {
			return nil, fmt.Errorf("%s intervalMs must be int", call.Callee.String())
		}
		interval = n
	}
	if timeout < 0 || interval <= 0 {
		return nil, fmt.Errorf("%s timeoutMs must be >= 0 and intervalMs must be > 0", call.Callee.String())
	}
	before := watchSnapshotValue(path.Value)
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	for {
		after := watchSnapshotValue(path.Value)
		if !watchSnapshotsEqual(before, after) {
			return watchResult(true, before, after), nil
		}
		if time.Now().After(deadline) || timeout == 0 {
			return watchResult(false, before, after), nil
		}
		time.Sleep(time.Duration(interval) * time.Millisecond)
	}
}

func watchSnapshotValue(path string) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "path", runtime.String{Value: path})
	info, err := os.Stat(path)
	if err != nil {
		putDict(entries, "exists", runtime.Bool{Value: false})
		putDict(entries, "size", runtime.NewInt64(0))
		putDict(entries, "mode", runtime.NewInt64(0))
		putDict(entries, "isDir", runtime.Bool{Value: false})
		putDict(entries, "modUnixNano", runtime.NewInt64(0))
		return runtime.Dict{Entries: entries}
	}
	putDict(entries, "exists", runtime.Bool{Value: true})
	putDict(entries, "size", runtime.NewInt64(info.Size()))
	putDict(entries, "mode", runtime.NewInt64(int64(info.Mode().Perm())))
	putDict(entries, "isDir", runtime.Bool{Value: info.IsDir()})
	putDict(entries, "modUnixNano", runtime.NewInt64(info.ModTime().UnixNano()))
	return runtime.Dict{Entries: entries}
}

func watchResult(changed bool, before runtime.Dict, after runtime.Dict) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "changed", runtime.Bool{Value: changed})
	putDict(entries, "before", before)
	putDict(entries, "after", after)
	return runtime.Dict{Entries: entries}
}

func watchSnapshotsEqual(left runtime.Dict, right runtime.Dict) bool {
	for _, key := range []string{"exists", "size", "mode", "isDir", "modUnixNano"} {
		leftValue, _ := dictField(left, key)
		rightValue, _ := dictField(right, key)
		if !valuesEqualSimple(leftValue, rightValue) {
			return false
		}
	}
	return true
}

// watchHandle owns an fsnotify watcher and the dispatch goroutine that
// invokes the user's callback on each event. The done channel is
// closed by stopWatchHandle to unblock the goroutine; the WaitGroup
// lets stop callers wait for in-flight callbacks to finish so reads
// from the parent goroutine happen-after the last callback write.
type watchHandle struct {
	watcher *fsnotify.Watcher
	done    chan struct{}
	wg      sync.WaitGroup
	stopped bool
}

// fsnotifyEventType maps fsnotify's bitmask to the protocol's
// "create" | "write" | "remove" | "rename" | "chmod" string.
func fsnotifyEventType(op fsnotify.Op) string {
	switch {
	case op.Has(fsnotify.Create):
		return "create"
	case op.Has(fsnotify.Write):
		return "write"
	case op.Has(fsnotify.Remove):
		return "remove"
	case op.Has(fsnotify.Rename):
		return "rename"
	case op.Has(fsnotify.Chmod):
		return "chmod"
	default:
		return "unknown"
	}
}

func (e *Evaluator) watchStart(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects path, callback, optional options", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	callback, ok := args[1].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("%s callback must be a function", call.Callee.String())
	}
	recursive := false
	if len(args) == 3 {
		opts, ok := args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s options must be a dict", call.Callee.String())
		}
		if value, found := dictField(opts, "recursive"); found {
			b, ok := value.(runtime.Bool)
			if !ok {
				return nil, fmt.Errorf("%s options.recursive must be bool", call.Callee.String())
			}
			recursive = b.Value
		}
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if recursive {
		if err := filepath.Walk(path.Value, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return watcher.Add(p)
			}
			return nil
		}); err != nil {
			_ = watcher.Close()
			return nil, err
		}
	} else {
		if err := watcher.Add(path.Value); err != nil {
			_ = watcher.Close()
			return nil, err
		}
	}
	handle := &watchHandle{watcher: watcher, done: make(chan struct{})}
	e.watchMu.Lock()
	e.nextWatchID++
	id := e.nextWatchID
	e.watches[id] = handle
	e.watchMu.Unlock()
	handle.wg.Add(1)
	go func() {
		defer handle.wg.Done()
		e.dispatchWatchEvents(handle, callback)
	}()
	return runtime.NewInt64(id), nil
}

// dispatchWatchEvents loops on the fsnotify channels until the handle
// is stopped, invoking the user's callback for each event. The
// callback runs in a child evaluator (for stack-frame isolation
// across goroutines) but the closure itself is NOT cloned, so
// mutations to captured globals propagate back to the parent. The
// `async.run` callback pattern uses the same approach.
func (e *Evaluator) dispatchWatchEvents(handle *watchHandle, callback runtime.Function) {
	child := e.childForCallback()
	defer child.Cleanup()
	for {
		select {
		case event, ok := <-handle.watcher.Events:
			if !ok {
				return
			}
			eventDict := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
			putDict(eventDict.Entries, "path", runtime.String{Value: event.Name})
			putDict(eventDict.Entries, "type", runtime.String{Value: fsnotifyEventType(event.Op)})
			_, _ = child.applyFunction(callback, []runtime.Value{eventDict})
		case _, ok := <-handle.watcher.Errors:
			if !ok {
				return
			}
		case <-handle.done:
			return
		}
	}
}

func (e *Evaluator) watchStop(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a watch handle", call.Callee.String())
	}
	id, err := rawInt64(args[0], "watch handle")
	if err != nil {
		return nil, err
	}
	e.watchMu.Lock()
	handle, ok := e.watches[id]
	if ok {
		delete(e.watches, id)
	}
	e.watchMu.Unlock()
	if !ok {
		if e.parent != nil {
			return e.parent.watchStop(call, args)
		}
		return runtime.Null{}, nil
	}
	return runtime.Null{}, stopWatchHandle(handle)
}

func stopWatchHandle(handle *watchHandle) error {
	if handle == nil || handle.stopped {
		return nil
	}
	handle.stopped = true
	close(handle.done)
	err := handle.watcher.Close()
	// Wait for the dispatch goroutine to finish its current callback
	// (and exit) before returning, so the caller's subsequent reads
	// from any callback-touched state happen after the last write.
	handle.wg.Wait()
	return err
}

func (e *Evaluator) logStdout(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	return e.registerLogger(&loggerHandle{target: "stdout", writer: e.stdout}), nil
}

func (e *Evaluator) logStderr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	return e.registerLogger(&loggerHandle{target: "stderr", writer: e.stderr}), nil
}

func (e *Evaluator) logFile(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return nil, err
	}
	return e.registerLogger(&loggerHandle{target: path, writer: file, closer: file}), nil
}

// logToStream returns a logger that writes JSON log lines to the
// given IOStream. Accepts either a raw IOStream native handle or
// an IOStream class instance (the wrapper from stdlib/streams.gb).
// The stream's lifetime is owned by the caller - closing the
// logger does NOT close the underlying stream, so the same stream
// can back multiple loggers or remain open for non-log traffic
// after the logger is discarded.
func (e *Evaluator) logToStream(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	streamValue := args[0]
	if inst, ok := streamValue.(*runtime.Instance); ok {
		// Unwrap a streams.IOStream class instance by reading its
		// `handle` field.
		if h, ok := inst.Fields["handle"]; ok {
			streamValue = h
		}
	}
	stream, err := e.ioStreamHandle(streamValue)
	if err != nil {
		return nil, err
	}
	if stream.writer == nil {
		return nil, fmt.Errorf("%s stream is not writable", call.Callee.String())
	}
	return e.registerLogger(&loggerHandle{target: stream.name, writer: stream.writer}), nil
}

func (e *Evaluator) logCustom(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	handler, ok := args[0].(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("%s handler must be object", call.Callee.String())
	}
	if _, ok := lookupMethod(handler.Class, "handle"); !ok {
		return nil, fmt.Errorf("%s handler must implement handle(level, message, fields)", call.Callee.String())
	}
	return e.registerLogger(&loggerHandle{target: handler.Class.Name, handler: handler}), nil
}

func (e *Evaluator) logInfo(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.logMessage(call, args, "info")
}

func (e *Evaluator) logWarn(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.logMessage(call, args, "warn")
}

func (e *Evaluator) logError(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.logMessage(call, args, "error")
}

func (e *Evaluator) logDebug(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	return e.logMessage(call, args, "debug")
}

func (e *Evaluator) logMessage(call *ast.CallExpression, args []runtime.Value, level string) (runtime.Value, error) {
	// Shorthand forms: `log.info("msg")` and `log.info("msg", {fields})`
	// route through a process-default stderr logger (created lazily). The
	// long form `log.info(logger, "msg" [, {fields}])` keeps existing
	// behaviour.
	if len(args) >= 1 {
		if _, isString := args[0].(runtime.String); isString {
			return e.logShorthand(call, args, level)
		}
	}
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects logger, message, and optional fields", call.Callee.String())
	}
	logger, err := e.loggerHandle(args[0])
	if err != nil {
		return nil, err
	}
	message, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s message must be string", call.Callee.String())
	}
	fields := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	if len(args) == 3 {
		var ok bool
		fields, ok = args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s fields must be dict", call.Callee.String())
		}
	}
	if logger.handler != nil {
		method, _ := lookupMethod(logger.handler.Class, "handle")
		_, err := e.applyFunctionWithThis(method, []runtime.Value{runtime.String{Value: level}, message, fields}, logger.handler)
		return runtime.Null{}, err
	}
	line, err := formatLogLine(level, message.Value, fields)
	if err != nil {
		return nil, err
	}
	_, err = io.WriteString(logger.writer, line+"\n")
	return runtime.Null{}, err
}

// logShorthand handles `log.<level>("msg")` and `log.<level>("msg",
// {fields})` by writing to a lazily-created default stderr logger.
func (e *Evaluator) logShorthand(call *ast.CallExpression, args []runtime.Value, level string) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects message and optional fields", call.Callee.String())
	}
	message, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s message must be string", call.Callee.String())
	}
	fields := runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	if len(args) == 2 {
		var ok bool
		fields, ok = args[1].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s fields must be dict", call.Callee.String())
		}
	}
	line, err := formatLogLine(level, message.Value, fields)
	if err != nil {
		return nil, err
	}
	_, err = io.WriteString(e.stderr, line+"\n")
	return runtime.Null{}, err
}

func (e *Evaluator) logClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	handle, err := logHandleID(args[0])
	if err != nil {
		return nil, err
	}
	e.logMu.Lock()
	logger, ok := e.loggers[handle]
	if ok {
		delete(e.loggers, handle)
	}
	e.logMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown logger handle %d", handle)
	}
	if logger.closer != nil {
		return runtime.Null{}, logger.closer.Close()
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) registerLogger(logger *loggerHandle) runtime.Value {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	e.nextLogID++
	e.loggers[e.nextLogID] = logger
	return runtime.NewInt64(e.nextLogID)
}

func (e *Evaluator) loggerHandle(value runtime.Value) (*loggerHandle, error) {
	handle, err := logHandleID(value)
	if err != nil {
		return nil, err
	}
	e.logMu.Lock()
	logger, ok := e.loggers[handle]
	e.logMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.loggerHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown logger handle %d", handle)
	}
	return logger, nil
}

func logHandleID(value runtime.Value) (int64, error) {
	id, ok := value.(runtime.Int)
	if !ok || !id.Value.IsInt64() {
		return 0, fmt.Errorf("logger handle must be int")
	}
	return id.Value.Int64(), nil
}

func formatLogLine(level string, message string, fields runtime.Dict) (string, error) {
	entries := map[string]any{
		"level":   level,
		"message": message,
		"time":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	fieldValues, err := valueToJSON(fields)
	if err != nil {
		return "", err
	}
	entries["fields"] = fieldValues
	data, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (e *Evaluator) testRun(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects test class and optional options", call.Callee.String())
	}
	if bytecodeClass, ok := args[0].(runtime.BytecodeClass); ok {
		if e.vmDispatcher == nil {
			return nil, fmt.Errorf("%s requires VM dispatcher for bytecode class", call.Callee.String())
		}
		var tagFilter []string
		if len(args) == 2 {
			options, err := testRunOptionsFromArgs(call, args)
			if err != nil {
				return nil, err
			}
			for tag := range options.tags {
				tagFilter = append(tagFilter, tag)
			}
		}
		return e.vmDispatcher.RunTestClass(bytecodeClass.Index, tagFilter)
	}
	class, ok := args[0].(*runtime.Class)
	if !ok {
		return nil, fmt.Errorf("%s expects a test class", call.Callee.String())
	}
	options, err := testRunOptionsFromArgs(call, args)
	if err != nil {
		return nil, err
	}
	instanceValue, err := e.instantiateClass(class, nil)
	if err != nil {
		return nil, err
	}
	instance := instanceValue.(*runtime.Instance)
	total := int64(0)
	passed := int64(0)
	failed := int64(0)
	failures := []runtime.Value{}
	tests := []runtime.Value{}
	methods := filterTestMethods(decoratedMethods(class, "test"), options.tags, options.methods)
	if err := e.applyOptionalTestHook(instance, "setupClass"); err != nil {
		failed = int64(len(methods))
		for _, method := range methods {
			total++
			failures = append(failures, runtime.String{Value: method.Name + ": setupClass: " + err.Error()})
			tests = append(tests, testCaseDict(method.Name, false, "setupClass: "+err.Error()))
		}
	} else {
		for _, method := range methods {
			total++
			/* Snapshot test.mock patches before each method so
			 * mocks from one test don't leak into the next.
			 * Both the evaluator's registry and the VM's (when
			 * present) need to roll back. */
			patchSnapshot := e.natives.Snapshot()
			var vmSnapshot map[string]native.Function
			if e.vmDispatcher != nil {
				vmSnapshot = e.vmDispatcher.NativeSnapshot()
			}
			testErr := e.applyOptionalTestHook(instance, "setup")
			if testErr == nil {
				_, testErr = e.applyFunctionWithThis(method, nil, instance)
			}
			if teardownErr := e.applyOptionalTestHook(instance, "teardown"); teardownErr != nil {
				if testErr != nil {
					testErr = fmt.Errorf("%v; teardown: %w", testErr, teardownErr)
				} else {
					testErr = fmt.Errorf("teardown: %w", teardownErr)
				}
			}
			e.natives.Restore(patchSnapshot)
			if e.vmDispatcher != nil {
				e.vmDispatcher.RestoreNatives(vmSnapshot)
			}
			if testErr != nil {
				failed++
				failures = append(failures, runtime.String{Value: method.Name + ": " + testErr.Error()})
				tests = append(tests, testCaseDict(method.Name, false, testErr.Error()))
				continue
			}
			passed++
			tests = append(tests, testCaseDict(method.Name, true, ""))
		}
	}
	if err := e.applyOptionalTestHook(instance, "teardownClass"); err != nil {
		failed++
		failures = append(failures, runtime.String{Value: "teardownClass: " + err.Error()})
		if passed > 0 {
			passed--
		}
		if total == 0 {
			total = 1
		}
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "total", runtime.NewInt64(total))
	putDict(entries, "passed", runtime.NewInt64(passed))
	putDict(entries, "failed", runtime.NewInt64(failed))
	putDict(entries, "failures", &runtime.List{Elements: failures})
	putDict(entries, "tests", &runtime.List{Elements: tests})
	return runtime.Dict{Entries: entries}, nil
}

// testMock(moduleName, {"fname": callable, ...}) installs patches
// on the registry shared by all native calls so subsequent
// invocations of those module functions dispatch to the user's
// callable instead. Patches roll back automatically at the end
// of each @test method via the snapshot/restore in testRun.
func (e *Evaluator) testMock(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (moduleName, dict<string, callable>)", call.Callee.String())
	}
	moduleName, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s moduleName must be a string", call.Callee.String())
	}
	replacements, ok := args[1].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s second argument must be a dict<string, callable>", call.Callee.String())
	}
	for _, entry := range replacements.Entries {
		fnameValue, ok := entry.Key.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s dict keys must be strings", call.Callee.String())
		}
		fname := fnameValue.Value
		callable := entry.Value
		patch := e.makeMockPatch(call, callable)
		e.natives.Patch(moduleName.Value, fname, patch)
		/* When running with the bytecode VM as the primary engine,
		 * the VM has its own registry; mirror the patch there so
		 * test.mock works on either dispatch path. */
		if e.vmDispatcher != nil {
			e.vmDispatcher.PatchNative(moduleName.Value, fname, patch)
		}
	}
	return runtime.Null{}, nil
}

// testRestore(module, fname) removes a single patch.
func (e *Evaluator) testRestore(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (moduleName, fname)", call.Callee.String())
	}
	moduleName, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s moduleName must be a string", call.Callee.String())
	}
	fname, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s fname must be a string", call.Callee.String())
	}
	e.natives.Unpatch(moduleName.Value, fname.Value)
	if e.vmDispatcher != nil {
		e.vmDispatcher.UnpatchNative(moduleName.Value, fname.Value)
	}
	return runtime.Null{}, nil
}

// testRestoreAll() clears every active patch. The test runner
// also calls this implicitly between methods.
func (e *Evaluator) testRestoreAll(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	e.natives.Restore(nil)
	if e.vmDispatcher != nil {
		e.vmDispatcher.RestoreNatives(nil)
	}
	return runtime.Null{}, nil
}

// makeMockPatch wraps a Geblang callable as a native.Function so
// the registry can dispatch native-call sites through it. The
// callable runs back through the evaluator via applyCallableValue.
func (e *Evaluator) makeMockPatch(call *ast.CallExpression, callable runtime.Value) native.Function {
	return func(args []runtime.Value) (runtime.Value, error) {
		return e.invokeMockCallable(call, callable, args)
	}
}

func (e *Evaluator) invokeMockCallable(call *ast.CallExpression, callable runtime.Value, args []runtime.Value) (runtime.Value, error) {
	switch fn := callable.(type) {
	case runtime.Function:
		return e.applyFunction(fn, args)
	case runtime.OverloadedFunction:
		for _, overload := range fn.Overloads {
			if len(overload.Parameters) == len(args) {
				return e.applyFunction(overload, args)
			}
		}
		return nil, fmt.Errorf("test.mock: no matching overload for %d arguments", len(args))
	}
	return nil, fmt.Errorf("test.mock: replacement is not callable")
}

// testCaseDict builds a per-test result entry with name, passed, and
// (for failures) a message. Used by the runner's verbose output path.
func testCaseDict(name string, passed bool, message string) runtime.Value {
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "name", runtime.String{Value: name})
	putDict(entries, "passed", runtime.Bool{Value: passed})
	if !passed {
		putDict(entries, "message", runtime.String{Value: message})
	}
	return runtime.Dict{Entries: entries}
}

type testRunOptions struct {
	tags    map[string]bool
	methods map[string]bool
}

func testRunOptionsFromArgs(call *ast.CallExpression, args []runtime.Value) (testRunOptions, error) {
	options := testRunOptions{}
	if len(args) == 1 {
		return options, nil
	}
	dict, ok := args[1].(runtime.Dict)
	if !ok {
		return options, fmt.Errorf("%s options must be dict", call.Callee.String())
	}
	if value, ok := dictField(dict, "tags"); ok {
		list, ok := value.(*runtime.List)
		if !ok {
			return options, fmt.Errorf("%s options.tags must be list<string>", call.Callee.String())
		}
		options.tags = map[string]bool{}
		for _, element := range list.Elements {
			tag, ok := element.(runtime.String)
			if !ok {
				return options, fmt.Errorf("%s options.tags must be list<string>", call.Callee.String())
			}
			options.tags[strings.ToLower(tag.Value)] = true
		}
	}
	if value, ok := dictField(dict, "methods"); ok {
		list, ok := value.(*runtime.List)
		if !ok {
			return options, fmt.Errorf("%s options.methods must be list<string>", call.Callee.String())
		}
		options.methods = map[string]bool{}
		for _, element := range list.Elements {
			name, ok := element.(runtime.String)
			if !ok {
				return options, fmt.Errorf("%s options.methods must be list<string>", call.Callee.String())
			}
			options.methods[name.Value] = true
		}
	}
	return options, nil
}

func filterTestMethods(methods []runtime.Function, tags map[string]bool, names map[string]bool) []runtime.Function {
	if len(tags) == 0 && len(names) == 0 {
		return methods
	}
	filtered := []runtime.Function{}
	for _, method := range methods {
		if len(names) > 0 && !names[method.Name] {
			continue
		}
		if len(tags) == 0 {
			filtered = append(filtered, method)
			continue
		}
		for _, tag := range testMethodTags(method) {
			if tags[strings.ToLower(tag)] {
				filtered = append(filtered, method)
				break
			}
		}
	}
	return filtered
}

func testMethodTags(method runtime.Function) []string {
	tags := []string{}
	for _, decorator := range method.Decorators {
		if !strings.EqualFold(decorator.Name.Value, "tag") {
			continue
		}
		for _, arg := range decorator.Arguments {
			if literal, ok := arg.Value.(*ast.StringLiteral); ok {
				tags = append(tags, literal.Value)
			}
		}
	}
	return tags
}

func (e *Evaluator) applyOptionalTestHook(instance *runtime.Instance, name string) error {
	method, ok := lookupMethod(instance.Class, name)
	if !ok {
		return nil
	}
	_, err := e.applyFunctionWithThis(method, nil, instance)
	return err
}

func decoratedMethods(class *runtime.Class, decorator string) []runtime.Function {
	methods := []runtime.Function{}
	seen := map[string]bool{}
	for current := class; current != nil; current = current.Parent {
		for key, overloads := range current.Methods {
			if seen[key] {
				continue
			}
			for _, method := range overloads {
				if !hasDecorator(method.Decorators, decorator) {
					continue
				}
				seen[key] = true
				methods = append(methods, method)
			}
		}
	}
	return methods
}

func hasDecorator(decorators []ast.Decorator, name string) bool {
	for _, decorator := range decorators {
		if strings.EqualFold(decorator.Name.Value, name) {
			return true
		}
	}
	return false
}

// classAbstractnessReason reports whether `class` cannot be
// instantiated directly. A class is abstract when it carries the
// @abstract class-level decorator, or any method declared on it or
// an ancestor carries @abstract and no more-derived class provides
// a concrete override.
func classAbstractnessReason(class *runtime.Class) (string, bool) {
	if class == nil {
		return "", false
	}
	if hasDecorator(class.Decorators, "abstract") {
		return "cannot instantiate abstract class " + class.Name, true
	}
	overridden := map[string]bool{}
	abstractDecl := map[string]string{}
	walk := func(c *runtime.Class) {
		for methodName, overloads := range c.Methods {
			isAbstract := false
			for _, fn := range overloads {
				if hasDecorator(fn.Decorators, "abstract") {
					isAbstract = true
					break
				}
			}
			if isAbstract {
				if !overridden[methodName] {
					if _, seen := abstractDecl[methodName]; !seen {
						abstractDecl[methodName] = c.Name
					}
				}
			} else {
				overridden[methodName] = true
				delete(abstractDecl, methodName)
			}
		}
	}
	walk(class)
	for c := class.Parent; c != nil; c = c.Parent {
		walk(c)
	}
	if len(abstractDecl) == 0 {
		return "", false
	}
	var sample, sampleClass string
	for name, owner := range abstractDecl {
		if sample == "" || name < sample {
			sample = name
			sampleClass = owner
		}
	}
	return "cannot instantiate " + class.Name + ": abstract method " + sampleClass + "." + sample + " is not implemented", true
}

func reflectDecorators(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects value and optional decorator name", call.Callee.String())
	}
	filter, err := optionalDecoratorName(call, args)
	if err != nil {
		return nil, err
	}
	if overloaded, ok := args[0].(runtime.OverloadedFunction); ok {
		return overloadedDecoratorListValue(overloaded, filter)
	}
	decorators, target, err := reflectTargetDecorators(call, args[0])
	if err != nil {
		return nil, err
	}
	return decoratorListValue(decorators, target, filter)
}

func reflectHasDecorator(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects value and decorator name", call.Callee.String())
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s decorator name must be string", call.Callee.String())
	}
	if overloaded, ok := args[0].(runtime.OverloadedFunction); ok {
		for _, overload := range overloaded.Overloads {
			if hasDecorator(overload.Decorators, name.Value) {
				return runtime.Bool{Value: true}, nil
			}
		}
		return runtime.Bool{Value: false}, nil
	}
	decorators, _, err := reflectTargetDecorators(call, args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Bool{Value: hasDecorator(decorators, name.Value)}, nil
}

func reflectDecorator(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects value and decorator name", call.Callee.String())
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s decorator name must be string", call.Callee.String())
	}
	if overloaded, ok := args[0].(runtime.OverloadedFunction); ok {
		for overloadIndex, overload := range overloaded.Overloads {
			target := reflectFunctionTarget(overload)
			for position, decorator := range overload.Decorators {
				if strings.EqualFold(decorator.Name.Value, name.Value) {
					return decoratorValue(decorator, target, position, overloadIndex)
				}
			}
		}
		return runtime.Null{}, nil
	}
	decorators, target, err := reflectTargetDecorators(call, args[0])
	if err != nil {
		return nil, err
	}
	for position, decorator := range decorators {
		if strings.EqualFold(decorator.Name.Value, name.Value) {
			return decoratorValue(decorator, target, position, 0)
		}
	}
	return runtime.Null{}, nil
}

func reflectParameters(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects value", call.Callee.String())
	}
	fn, ok := reflectFunctionMetadataValue(args[0])
	if !ok {
		return nil, fmt.Errorf("%s expects function or method, got %s", call.Callee.String(), args[0].TypeName())
	}
	values := make([]runtime.Value, 0, len(fn.Parameters))
	for _, parameter := range fn.Parameters {
		values = append(values, parameterMetadataValue(parameter))
	}
	return &runtime.List{Elements: values}, nil
}

func reflectReturnType(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects value", call.Callee.String())
	}
	fn, ok := reflectFunctionMetadataValue(args[0])
	if !ok {
		return nil, fmt.Errorf("%s expects function or method, got %s", call.Callee.String(), args[0].TypeName())
	}
	return runtime.String{Value: fn.ReturnType}, nil
}

func reflectDoc(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	doc, ok, err := reflectDocText(call, args)
	if err != nil || !ok {
		return nil, err
	}
	if doc == "" {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: doc}, nil
}

func reflectDocs(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	doc, ok, err := reflectDocText(call, args)
	if err != nil || !ok {
		return nil, err
	}
	if doc == "" {
		return runtime.Null{}, nil
	}
	return docMetadataValue(doc), nil
}

func reflectDocText(call *ast.CallExpression, args []runtime.Value) (string, bool, error) {
	if len(args) != 1 {
		return "", false, fmt.Errorf("%s expects value", call.Callee.String())
	}
	if fn, ok := reflectFunctionMetadataValue(args[0]); ok {
		return fn.Doc, true, nil
	}
	if metadata, err := reflectClassMetadataValue(call, args); err == nil {
		return metadata.Doc, true, nil
	}
	if iface, ok := args[0].(*runtime.Interface); ok {
		return iface.Doc, true, nil
	}
	return "", false, fmt.Errorf("%s expects function, method, class, or interface, got %s", call.Callee.String(), args[0].TypeName())
}

func docMetadataValue(doc string) runtime.Dict {
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	lineValues := make([]runtime.Value, 0, len(lines))
	for _, line := range lines {
		lineValues = append(lineValues, runtime.String{Value: line})
	}
	summary := ""
	summaryIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			summary = strings.TrimSpace(line)
			summaryIndex = i
			break
		}
	}
	body := ""
	if summaryIndex >= 0 && summaryIndex+1 < len(lines) {
		body = strings.TrimSpace(strings.Join(lines[summaryIndex+1:], "\n"))
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "text", runtime.String{Value: doc})
	putDict(entries, "summary", runtime.String{Value: summary})
	putDict(entries, "body", runtime.String{Value: body})
	putDict(entries, "lines", &runtime.List{Elements: lineValues})
	return runtime.Dict{Entries: entries}
}

func reflectExports(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects module", call.Callee.String())
	}
	module, ok := args[0].(*runtime.Module)
	if !ok {
		return nil, fmt.Errorf("%s expects module, got %s", call.Callee.String(), args[0].TypeName())
	}
	return stringList(dirValue(module)), nil
}

func reflectTypeOf(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects value", call.Callee.String())
	}
	return runtime.Type{Name: args[0].TypeName()}, nil
}

// reflectLocation returns the source position of a function or class
// declaration as `{module, line, column}`. Returns null when the
// value carries no recorded location.
func reflectLocation(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects value", call.Callee.String())
	}
	makeDict := func(module string, line, column int64) runtime.Dict {
		entries := map[string]runtime.DictEntry{}
		putDict(entries, "module", runtime.String{Value: module})
		putDict(entries, "line", runtime.NewInt64(line))
		putDict(entries, "column", runtime.NewInt64(column))
		return runtime.Dict{Entries: entries}
	}
	switch v := args[0].(type) {
	case runtime.DecoratorTarget:
		if v.Function != nil && (v.Function.DefLine != 0 || v.Function.DefColumn != 0) {
			return makeDict(v.Function.Module, v.Function.DefLine, v.Function.DefColumn), nil
		}
		if v.Class != nil && (v.Class.DefLine != 0 || v.Class.DefColumn != 0) {
			return makeDict(v.Class.Module, v.Class.DefLine, v.Class.DefColumn), nil
		}
	case runtime.Function:
		if v.DefinitionLine != 0 || v.DefinitionColumn != 0 {
			return makeDict(v.DefinitionModule, int64(v.DefinitionLine), int64(v.DefinitionColumn)), nil
		}
	case *runtime.Class:
		if v != nil && (v.DefinitionLine != 0 || v.DefinitionColumn != 0) {
			return makeDict(v.DefinitionModule, int64(v.DefinitionLine), int64(v.DefinitionColumn)), nil
		}
	case *runtime.Instance:
		if v != nil && v.Class != nil && (v.Class.DefinitionLine != 0 || v.Class.DefinitionColumn != 0) {
			return makeDict(v.Class.DefinitionModule, int64(v.Class.DefinitionLine), int64(v.Class.DefinitionColumn)), nil
		}
	}
	return runtime.Null{}, nil
}

func reflectFields(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects class", call.Callee.String())
	}
	// Prefer structured field metadata - matches the
	// `reflect.parameters` shape (each entry {name, type, nullable,
	// hasDefault}). When the value is a *runtime.Class or an
	// instance we can read the actual Field structs; for
	// bytecode-class or compile-time metadata we fall back to the
	// name-only list wrapped in dicts.
	if cls, ok := classForReflectFields(args[0]); ok && cls != nil {
		entries := make([]runtime.Value, 0, len(cls.Fields))
		for _, field := range cls.Fields {
			fd := map[string]runtime.DictEntry{}
			putDict(fd, "name", runtime.String{Value: field.Name})
			putDict(fd, "type", runtime.String{Value: typeRefToString(field.Type)})
			nullable := false
			if field.Type != nil {
				nullable = field.Type.Nullable
			}
			putDict(fd, "nullable", runtime.Bool{Value: nullable})
			putDict(fd, "hasDefault", runtime.Bool{Value: field.Default != nil})
			decs, derr := decoratorListValue(field.Decorators, "field", "")
			if derr == nil {
				putDict(fd, "decorators", decs)
			}
			entries = append(entries, runtime.Dict{Entries: fd})
		}
		sort.SliceStable(entries, func(i, j int) bool {
			a := entries[i].(runtime.Dict).Entries[native.DictKey(runtime.String{Value: "name"})].Value.(runtime.String).Value
			b := entries[j].(runtime.Dict).Entries[native.DictKey(runtime.String{Value: "name"})].Value.(runtime.String).Value
			return a < b
		})
		return &runtime.List{Elements: entries}, nil
	}
	metadata, err := reflectClassMetadataValue(call, args)
	if err != nil {
		return nil, err
	}
	entries := make([]runtime.Value, 0, len(metadata.Fields))
	for _, name := range metadata.Fields {
		fd := map[string]runtime.DictEntry{}
		putDict(fd, "name", runtime.String{Value: name})
		putDict(fd, "type", runtime.String{Value: "any"})
		putDict(fd, "nullable", runtime.Bool{Value: false})
		putDict(fd, "hasDefault", runtime.Bool{Value: false})
		entries = append(entries, runtime.Dict{Entries: fd})
	}
	return &runtime.List{Elements: entries}, nil
}

func classForReflectFields(v runtime.Value) (*runtime.Class, bool) {
	switch x := v.(type) {
	case *runtime.Class:
		return x, true
	case *runtime.Instance:
		if x != nil {
			return x.Class, true
		}
	}
	return nil, false
}

func typeRefToString(t *ast.TypeRef) string {
	if t == nil {
		return "any"
	}
	return t.String()
}

func reflectMethods(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	metadata, err := reflectClassMetadataValue(call, args)
	if err != nil {
		return nil, err
	}
	methods := append([]string(nil), metadata.Methods...)
	sort.Strings(methods)
	return stringList(methods), nil
}

func reflectStaticMethods(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	metadata, err := reflectClassMetadataValue(call, args)
	if err != nil {
		return nil, err
	}
	return stringList(metadata.StaticMethods), nil
}

func reflectParent(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	metadata, err := reflectClassMetadataValue(call, args)
	if err != nil {
		return nil, err
	}
	if metadata.Parent == "" {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: metadata.Parent}, nil
}

// reflectGetField reads a single field off an instance by name.
// Returns null when the field doesn't exist on the instance's
// class (rather than erroring) so callers driving framework-style
// reflection don't need a separate `hasField` probe.
func reflectGetField(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (instance, fieldName)", call.Callee.String())
	}
	instance, ok := args[0].(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("%s expects instance, got %s", call.Callee.String(), args[0].TypeName())
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s field name must be string", call.Callee.String())
	}
	if v, hit := instance.Fields[name.Value]; hit {
		return v, nil
	}
	return runtime.Null{}, nil
}

// reflectSetField assigns a value to a named field on an instance.
// Returns the same instance (allowing fluent chaining). Field
// existence is not validated up-front: the assign succeeds and the
// field becomes part of the instance's field map. This matches the
// permissive shape that framework code (Gebweb's @Assert /
// @ApiResource PATCH) needs.
func reflectSetField(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects (instance, fieldName, value)", call.Callee.String())
	}
	instance, ok := args[0].(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("%s expects instance, got %s", call.Callee.String(), args[0].TypeName())
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s field name must be string", call.Callee.String())
	}
	if instance.Fields == nil {
		instance.Fields = map[string]runtime.Value{}
	}
	instance.Fields[name.Value] = args[2]
	return instance, nil
}

// reflectClassName returns the class's own name regardless of whether
// the argument is a class value, an instance, or a primitive. For a
// class value `reflect.typeOf` returns the meta-string "class" - this
// builtin returns the class's actual identifier (e.g. "UserRepo").
// Returns null when the argument carries no class identity (closures,
// modules, ...). Symmetric with `reflect.class(name)` which goes the
// other way.
func reflectClassName(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	metadata, err := reflectClassMetadataValue(call, args)
	if err == nil && metadata.Name != "" {
		return runtime.String{Value: metadata.Name}, nil
	}
	/* className is total: for primitives and other values without
	 * class metadata, return the runtime type name (symmetric with
	 * how reflect.typeOf handles instances). */
	return runtime.String{Value: args[0].TypeName()}, nil
}

func reflectInterfaces(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	metadata, err := reflectClassMetadataValue(call, args)
	if err != nil {
		return nil, err
	}
	return stringList(metadata.Interfaces), nil
}

func reflectConstructors(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects class", call.Callee.String())
	}
	switch value := args[0].(type) {
	case *runtime.Class:
		overloads := make([]runtime.Value, 0, len(value.Constructors))
		for _, ctor := range value.Constructors {
			md := functionMetadataFromRuntimeFunction(ctor)
			paramValues := make([]runtime.Value, 0, len(md.Parameters))
			for _, p := range md.Parameters {
				paramValues = append(paramValues, parameterMetadataValue(p))
			}
			overloads = append(overloads, &runtime.List{Elements: paramValues})
		}
		return &runtime.List{Elements: overloads}, nil
	case runtime.BytecodeClass:
		overloads := make([]runtime.Value, 0, len(value.ConstructorMetadata))
		for _, md := range value.ConstructorMetadata {
			paramValues := make([]runtime.Value, 0, len(md.Parameters))
			for _, p := range md.Parameters {
				paramValues = append(paramValues, parameterMetadataValue(p))
			}
			overloads = append(overloads, &runtime.List{Elements: paramValues})
		}
		return &runtime.List{Elements: overloads}, nil
	case runtime.DecoratorTarget:
		if value.Class != nil {
			return &runtime.List{Elements: []runtime.Value{}}, nil
		}
	}
	return nil, fmt.Errorf("%s expects class, got %s", call.Callee.String(), args[0].TypeName())
}

func reflectTypeBindings(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects instance", call.Callee.String())
	}
	entries := map[string]runtime.DictEntry{}
	switch v := args[0].(type) {
	case *runtime.Instance:
		for name, typeName := range v.TypeBindings {
			putDict(entries, name, runtime.String{Value: typeName})
		}
	case *runtime.List:
		if len(v.ElementTypes) >= 1 {
			putDict(entries, "T", runtime.String{Value: v.ElementTypes[0]})
		}
	case runtime.Set:
		if len(v.ElementTypes) >= 1 {
			putDict(entries, "T", runtime.String{Value: v.ElementTypes[0]})
		}
	case runtime.Dict:
		if len(v.ElementTypes) >= 2 {
			putDict(entries, "K", runtime.String{Value: v.ElementTypes[0]})
			putDict(entries, "V", runtime.String{Value: v.ElementTypes[1]})
		}
	default:
		return nil, fmt.Errorf("%s expects instance or generic collection, got %s", call.Callee.String(), args[0].TypeName())
	}
	return runtime.Dict{Entries: entries}, nil
}

func reflectInterfaceMethods(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects interface", call.Callee.String())
	}
	iface, ok := args[0].(*runtime.Interface)
	if !ok {
		return nil, fmt.Errorf("%s expects interface, got %s", call.Callee.String(), args[0].TypeName())
	}
	type methodEntry struct {
		name string
		val  runtime.Value
	}
	methods := make([]methodEntry, 0, len(iface.Methods))
	for _, sig := range iface.Methods {
		name := ""
		if sig.Name != nil {
			name = sig.Name.Value
		}
		params := make([]runtime.Value, 0, len(sig.Parameters))
		for _, param := range sig.Parameters {
			pname := ""
			if param.Name != nil {
				pname = param.Name.Value
			}
			ptype := "any"
			if param.Type != nil {
				ptype = param.Type.String()
			}
			pm := runtime.ParameterMetadata{
				Name:       pname,
				Type:       ptype,
				Variadic:   param.Variadic,
				HasDefault: param.Default != nil,
				Decorators: decoratorsMetadataFromAST(param.Decorators, "parameter"),
			}
			params = append(params, parameterMetadataValue(pm))
		}
		rt := "void"
		if sig.ReturnType != nil {
			rt = sig.ReturnType.String()
		}
		entries := map[string]runtime.DictEntry{}
		putDict(entries, "name", runtime.String{Value: name})
		if sig.Doc == "" {
			putDict(entries, "doc", runtime.Null{})
		} else {
			putDict(entries, "doc", runtime.String{Value: sig.Doc})
		}
		putDict(entries, "parameters", &runtime.List{Elements: params})
		putDict(entries, "returnType", runtime.String{Value: rt})
		methods = append(methods, methodEntry{name: strings.ToLower(name), val: runtime.Dict{Entries: entries}})
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].name < methods[j].name })
	values := make([]runtime.Value, len(methods))
	for i, m := range methods {
		values[i] = m.val
	}
	return &runtime.List{Elements: values}, nil
}

func reflectInterfaceParents(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects interface", call.Callee.String())
	}
	iface, ok := args[0].(*runtime.Interface)
	if !ok {
		return nil, fmt.Errorf("%s expects interface, got %s", call.Callee.String(), args[0].TypeName())
	}
	names := make([]string, 0, len(iface.Parents))
	for _, parent := range iface.Parents {
		names = append(names, parent.Name)
	}
	sort.Strings(names)
	return stringList(names), nil
}

func reflectClassMetadataValue(call *ast.CallExpression, args []runtime.Value) (runtime.ClassMetadata, error) {
	if len(args) != 1 {
		return runtime.ClassMetadata{}, fmt.Errorf("%s expects class", call.Callee.String())
	}
	switch value := args[0].(type) {
	case *runtime.Class:
		return classMetadataFromRuntimeClass(value), nil
	case *runtime.Instance:
		// Accept an instance for symmetry with the VM: framework
		// code that holds an instance shouldn't have to recover the
		// class from its name.
		if value != nil && value.Class != nil {
			return classMetadataFromRuntimeClass(value.Class), nil
		}
	case runtime.Error:
		// Error-derived class instances are wrapped as runtime.Error.
		// The evaluator-aware variant (e.errorClassMetadataValue) can
		// follow the cross-module registry; this free function only
		// returns the class name when nothing else is available.
		return runtime.ClassMetadata{Name: value.Class}, nil
	case runtime.DecoratorTarget:
		if value.Class != nil {
			return *value.Class, nil
		}
	case runtime.BytecodeClass:
		return classMetadataFromBytecodeClass(value), nil
	}
	// Built-in primitive reflection: list / dict / set / string /
	// bytes / range expose their method table via the curated table
	// in primitiveTypeMetadata. Last-resort lookup so interface
	// names (passed in as strings from the compile-time path) get a
	// chance at their proper handler upstream.
	if md, ok := primitiveTypeMetadata(args[0]); ok {
		return md, nil
	}
	return runtime.ClassMetadata{}, fmt.Errorf("%s expects class, got %s", call.Callee.String(), args[0].TypeName())
}

// primitiveTypeMetadata returns a synthetic ClassMetadata describing the
// method surface of a built-in primitive value (list, dict, set, string,
// bytes, range). The reflect.* API uses this so framework code can
// introspect primitives the same way it introspects user-defined classes.
func primitiveTypeMetadata(value runtime.Value) (runtime.ClassMetadata, bool) {
	switch value.(type) {
	case *runtime.List:
		return runtime.ClassMetadata{
			Name:    "list",
			Methods: primitiveMethodNamesFor("list"),
		}, true
	case runtime.Dict:
		return runtime.ClassMetadata{
			Name:    "dict",
			Methods: primitiveMethodNamesFor("dict"),
		}, true
	case runtime.Set:
		return runtime.ClassMetadata{
			Name:    "set",
			Methods: primitiveMethodNamesFor("set"),
		}, true
	case runtime.String:
		return runtime.ClassMetadata{
			Name:    "string",
			Methods: primitiveMethodNamesFor("string"),
		}, true
	case runtime.Bytes:
		return runtime.ClassMetadata{
			Name:    "bytes",
			Methods: primitiveMethodNamesFor("bytes"),
		}, true
	case runtime.Range:
		return runtime.ClassMetadata{
			Name:    "range",
			Methods: primitiveMethodNamesFor("range"),
		}, true
	}
	return runtime.ClassMetadata{}, false
}

// primitiveMethodNamesFor returns a sorted list of method names for a
// primitive type. The list is curated rather than introspected from the
// dispatch tables because some method names share an implementation and
// some have different effective surfaces per type.
func primitiveMethodNamesFor(typeName string) []string {
	return append([]string(nil), native.PrimitiveMethods[typeName]...)
}

func classMetadataFromRuntimeClass(class *runtime.Class) runtime.ClassMetadata {
	metadata := runtime.ClassMetadata{Name: class.Name, Doc: class.Doc}
	if class.Parent != nil {
		metadata.Parent = class.Parent.Name
	}
	for _, field := range class.Fields {
		metadata.Fields = append(metadata.Fields, field.Name)
	}
	methods := map[string]string{}
	for name, overloads := range class.Methods {
		methods[name] = reflectedFunctionName(name, overloads)
	}
	staticMethods := map[string]string{}
	for name, overloads := range class.StaticMethods {
		staticMethods[name] = reflectedFunctionName(name, overloads)
	}
	for _, iface := range class.Implements {
		metadata.Interfaces = append(metadata.Interfaces, iface.Name)
	}
	sort.Strings(metadata.Fields)
	metadata.Methods = sortedStringMapValues(methods)
	metadata.StaticMethods = sortedStringMapValues(staticMethods)
	sort.Strings(metadata.Interfaces)
	return metadata
}

func classMetadataFromBytecodeClass(class runtime.BytecodeClass) runtime.ClassMetadata {
	methods := map[string]string{}
	for name, overloads := range class.MethodMetadata {
		methods[name] = reflectedFunctionMetadataName(name, overloads)
	}
	staticMethods := map[string]string{}
	for name, overloads := range class.StaticMetadata {
		staticMethods[name] = reflectedFunctionMetadataName(name, overloads)
	}
	metadata := runtime.ClassMetadata{
		Name:          class.Name,
		Doc:           class.Doc,
		Parent:        class.Parent,
		Fields:        append([]string(nil), class.Fields...),
		Methods:       sortedStringMapValues(methods),
		StaticMethods: sortedStringMapValues(staticMethods),
		Interfaces:    append([]string(nil), class.Interfaces...),
	}
	sort.Strings(metadata.Fields)
	sort.Strings(metadata.Interfaces)
	return metadata
}

func reflectedFunctionName(fallback string, overloads []runtime.Function) string {
	if len(overloads) > 0 && overloads[0].Name != "" {
		return overloads[0].Name
	}
	return fallback
}

func reflectedFunctionMetadataName(fallback string, overloads []runtime.FunctionMetadata) string {
	if len(overloads) > 0 && overloads[0].Name != "" {
		return overloads[0].Name
	}
	return fallback
}

func sortedStringMapValues(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func reflectLookupRequiresEvaluator(call *ast.CallExpression, _ []runtime.Value) (runtime.Value, error) {
	return nil, fmt.Errorf("%s requires evaluator context", call.Callee.String())
}

func reflectMethod(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects class or instance and method name", call.Callee.String())
	}
	instance, bound := args[0].(*runtime.Instance)
	class, err := reflectClassArg(call, args[0])
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s method name must be string", call.Callee.String())
	}
	overloads, ok := class.Methods[strings.ToLower(name.Value)]
	if !ok {
		return runtime.Null{}, nil
	}
	if bound {
		return boundReflectMethodFunction(class.Name+"."+name.Value, overloads, instance, nil), nil
	}
	return reflectFunctionValue(name.Value, overloads), nil
}

// reflectMethodBound is the evaluator-bound variant. It captures the live
// Evaluator so the returned bound method runs against the same module
// loader / state instead of a fresh stub Evaluator that can't resolve
// imported modules (gebweb.notFound, etc.).
func (e *Evaluator) reflectMethodBound(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects class or instance and method name", call.Callee.String())
	}
	instance, bound := args[0].(*runtime.Instance)
	class, err := reflectClassArg(call, args[0])
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s method name must be string", call.Callee.String())
	}
	overloads, ok := class.Methods[strings.ToLower(name.Value)]
	if !ok {
		return runtime.Null{}, nil
	}
	if bound {
		return boundReflectMethodFunction(class.Name+"."+name.Value, overloads, instance, e), nil
	}
	return reflectFunctionValue(name.Value, overloads), nil
}

func boundReflectMethodFunction(label string, overloads []runtime.Function, instance *runtime.Instance, host *Evaluator) runtime.Function {
	metadataSource := runtime.Function{}
	if len(overloads) > 0 {
		metadataSource = overloads[0]
	}
	return runtime.Function{
		Name:       metadataSource.Name,
		Parameters: append([]ast.Parameter(nil), metadataSource.Parameters...),
		ReturnType: metadataSource.ReturnType,
		Decorators: append([]ast.Decorator(nil), metadataSource.Decorators...),
		Target:     reflectFunctionTarget(metadataSource),
		Async:      metadataSource.Async,
		Native: func(this *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
			method, err := selectOverload(label, overloads, args)
			if err != nil {
				return nil, err
			}
			ev := host
			if ev == nil {
				ev = evaluatorForNativeMethod(method)
			}
			return ev.applyFunctionWithThis(method, args, instance)
		},
	}
}

func evaluatorForNativeMethod(fn runtime.Function) *Evaluator {
	return &Evaluator{stdout: io.Discard, maxCallDepth: DefaultMaxCallDepth}
}

func reflectStaticMethod(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects class and static method name", call.Callee.String())
	}
	class, ok := args[0].(*runtime.Class)
	if !ok {
		return nil, fmt.Errorf("%s expects class, got %s", call.Callee.String(), args[0].TypeName())
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s static method name must be string", call.Callee.String())
	}
	overloads, ok := class.StaticMethods[strings.ToLower(name.Value)]
	if !ok {
		return runtime.Null{}, nil
	}
	return reflectFunctionValue(name.Value, overloads), nil
}

func reflectClassArg(call *ast.CallExpression, value runtime.Value) (*runtime.Class, error) {
	switch value := value.(type) {
	case *runtime.Class:
		return value, nil
	case *runtime.Instance:
		return value.Class, nil
	default:
		return nil, fmt.Errorf("%s expects class or instance, got %s", call.Callee.String(), value.TypeName())
	}
}

func reflectFunctionValue(name string, overloads []runtime.Function) runtime.Value {
	if len(overloads) == 1 {
		return overloads[0]
	}
	return runtime.OverloadedFunction{Name: name, Overloads: append([]runtime.Function(nil), overloads...)}
}

func reflectTargetDecorators(call *ast.CallExpression, value runtime.Value) ([]ast.Decorator, string, error) {
	switch value := value.(type) {
	case runtime.Function:
		return value.Decorators, reflectFunctionTarget(value), nil
	case *runtime.Class:
		return value.Decorators, "class", nil
	case runtime.DecoratorTarget:
		return nil, value.Target, nil
	default:
		return nil, "", fmt.Errorf("%s expects function or class, got %s", call.Callee.String(), value.TypeName())
	}
}

func reflectFunctionTarget(fn runtime.Function) string {
	if fn.Target != "" {
		return fn.Target
	}
	return "function"
}

func reflectFunctionMetadataValue(value runtime.Value) (runtime.FunctionMetadata, bool) {
	switch value := value.(type) {
	case runtime.Function:
		return functionMetadataFromRuntimeFunction(value), true
	case runtime.OverloadedFunction:
		if len(value.Overloads) == 0 {
			return runtime.FunctionMetadata{}, false
		}
		return functionMetadataFromRuntimeFunction(value.Overloads[0]), true
	case runtime.DecoratorTarget:
		if value.Function != nil {
			return *value.Function, true
		}
	case runtime.BytecodeFunction:
		return runtime.FunctionMetadata{
			Name:       value.Name,
			Target:     "function",
			Doc:        value.Doc,
			Parameters: append([]runtime.ParameterMetadata(nil), value.Parameters...),
			ReturnType: value.ReturnType,
			Async:      value.Async,
			Variadic:   value.Variadic,
			Decorators: append([]runtime.DecoratorMetadata(nil), value.Decorators...),
		}, true
	}
	return runtime.FunctionMetadata{}, false
}

func functionMetadataFromRuntimeFunction(fn runtime.Function) runtime.FunctionMetadata {
	parameters := make([]runtime.ParameterMetadata, 0, len(fn.Parameters))
	for _, param := range fn.Parameters {
		name := ""
		if param.Name != nil {
			name = param.Name.Value
		}
		typ := "any"
		if param.Type != nil {
			typ = param.Type.String()
		}
		parameters = append(parameters, runtime.ParameterMetadata{
			Name:       name,
			Type:       typ,
			Variadic:   param.Variadic,
			HasDefault: param.Default != nil,
			Decorators: decoratorsMetadataFromAST(param.Decorators, "parameter"),
		})
	}
	returnType := "void"
	if fn.ReturnType != nil {
		returnType = fn.ReturnType.String()
	}
	return runtime.FunctionMetadata{
		Name:       fn.Name,
		Target:     reflectFunctionTarget(fn),
		Doc:        fn.Doc,
		Parameters: parameters,
		ReturnType: returnType,
		Async:      fn.Async,
		Variadic:   len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Variadic,
	}
}

func optionalDecoratorName(call *ast.CallExpression, args []runtime.Value) (string, error) {
	if len(args) == 1 {
		return "", nil
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s decorator name must be string", call.Callee.String())
	}
	return name.Value, nil
}

func decoratorListValue(decorators []ast.Decorator, target string, filter string) (runtime.Value, error) {
	values := make([]runtime.Value, 0, len(decorators))
	for position, decorator := range decorators {
		if filter != "" && !strings.EqualFold(decorator.Name.Value, filter) {
			continue
		}
		value, err := decoratorValue(decorator, target, position, 0)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return &runtime.List{Elements: values}, nil
}

func overloadedDecoratorListValue(overloaded runtime.OverloadedFunction, filter string) (runtime.Value, error) {
	values := []runtime.Value{}
	for overloadIndex, overload := range overloaded.Overloads {
		target := reflectFunctionTarget(overload)
		for position, decorator := range overload.Decorators {
			if filter != "" && !strings.EqualFold(decorator.Name.Value, filter) {
				continue
			}
			value, err := decoratorValue(decorator, target, position, overloadIndex)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
	}
	return &runtime.List{Elements: values}, nil
}

func decoratorValue(decorator ast.Decorator, target string, position int, overload int) (runtime.Value, error) {
	entries := map[string]runtime.DictEntry{}
	name := ""
	if decorator.Name != nil {
		name = decorator.Name.Value
	}
	putDict(entries, "name", runtime.String{Value: name})
	putDict(entries, "target", runtime.String{Value: target})
	putDict(entries, "position", runtime.NewInt64(int64(position)))
	putDict(entries, "overload", runtime.NewInt64(int64(overload)))
	args := []runtime.Value{}
	namedArgs := map[string]runtime.DictEntry{}
	for _, arg := range decorator.Arguments {
		if arg.Spread {
			return nil, fmt.Errorf("decorator %s metadata does not support spread arguments", name)
		}
		value, err := decoratorMetadataValue(arg.Value)
		if err != nil {
			return nil, fmt.Errorf("decorator %s metadata: %w", name, err)
		}
		if arg.Name != nil {
			putDict(namedArgs, arg.Name.Value, value)
		} else {
			args = append(args, value)
		}
	}
	putDict(entries, "args", &runtime.List{Elements: args})
	putDict(entries, "namedArgs", runtime.Dict{Entries: namedArgs})
	putDict(entries, "line", runtime.NewInt64(int64(decorator.Token.Line)))
	putDict(entries, "column", runtime.NewInt64(int64(decorator.Token.Column)))
	return runtime.Dict{Entries: entries}, nil
}

func parameterMetadataValue(parameter runtime.ParameterMetadata) runtime.Value {
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "name", runtime.String{Value: parameter.Name})
	putDict(entries, "type", runtime.String{Value: parameter.Type})
	putDict(entries, "variadic", runtime.Bool{Value: parameter.Variadic})
	putDict(entries, "hasDefault", runtime.Bool{Value: parameter.HasDefault})
	if len(parameter.Decorators) > 0 {
		decValues := make([]runtime.Value, 0, len(parameter.Decorators))
		for _, dec := range parameter.Decorators {
			decValues = append(decValues, decoratorMetadataDictValue(dec))
		}
		putDict(entries, "decorators", &runtime.List{Elements: decValues})
	}
	return runtime.Dict{Entries: entries}
}

func decoratorMetadataDictValue(metadata runtime.DecoratorMetadata) runtime.Value {
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "name", runtime.String{Value: metadata.Name})
	putDict(entries, "target", runtime.String{Value: metadata.Target})
	putDict(entries, "position", runtime.NewInt64(metadata.Position))
	putDict(entries, "overload", runtime.NewInt64(metadata.Overload))
	args := make([]runtime.Value, 0, len(metadata.Args))
	args = append(args, metadata.Args...)
	putDict(entries, "args", &runtime.List{Elements: args})
	namedArgs := map[string]runtime.DictEntry{}
	for k, v := range metadata.NamedArgs {
		putDict(namedArgs, k, v)
	}
	putDict(entries, "namedArgs", runtime.Dict{Entries: namedArgs})
	putDict(entries, "line", runtime.NewInt64(metadata.Line))
	putDict(entries, "column", runtime.NewInt64(metadata.Column))
	return runtime.Dict{Entries: entries}
}

func decoratorsMetadataFromAST(decorators []ast.Decorator, target string) []runtime.DecoratorMetadata {
	if len(decorators) == 0 {
		return nil
	}
	out := make([]runtime.DecoratorMetadata, 0, len(decorators))
	for position, dec := range decorators {
		item := runtime.DecoratorMetadata{
			Target:    target,
			Position:  int64(position),
			Line:      int64(dec.Token.Line),
			Column:    int64(dec.Token.Column),
			NamedArgs: map[string]runtime.Value{},
		}
		if dec.Name != nil {
			item.Name = dec.Name.Value
		}
		for _, arg := range dec.Arguments {
			value, err := decoratorMetadataValue(arg.Value)
			if err != nil {
				continue
			}
			if arg.Name != nil {
				item.NamedArgs[arg.Name.Value] = value
			} else {
				item.Args = append(item.Args, value)
			}
		}
		out = append(out, item)
	}
	return out
}

func decoratorMetadataValue(expr ast.Expression) (runtime.Value, error) {
	switch expr := expr.(type) {
	case *ast.StringLiteral:
		return runtime.String{Value: expr.Value}, nil
	case *ast.IntegerLiteral:
		return runtime.NewIntLiteral(expr.Value)
	case *ast.DecimalLiteral:
		return runtime.NewDecimalLiteral(expr.Value)
	case *ast.FloatLiteral:
		stripped := strings.ReplaceAll(expr.Value[:len(expr.Value)-1], "_", "")
		value, err := strconv.ParseFloat(stripped, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float literal %q", expr.Value)
		}
		return runtime.Float{Value: value}, nil
	case *ast.Literal:
		switch value := expr.Value.(type) {
		case bool:
			return runtime.Bool{Value: value}, nil
		case nil:
			return runtime.Null{}, nil
		default:
			return nil, fmt.Errorf("unsupported literal %v", expr.Value)
		}
	case *ast.ListLiteral:
		values := make([]runtime.Value, 0, len(expr.Elements))
		for _, element := range expr.Elements {
			value, err := decoratorMetadataValue(element)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return &runtime.List{Elements: values}, nil
	case *ast.DictLiteral:
		entries := map[string]runtime.DictEntry{}
		for _, entry := range expr.Entries {
			key, err := decoratorMetadataValue(entry.Key)
			if err != nil {
				return nil, err
			}
			value, err := decoratorMetadataValue(entry.Value)
			if err != nil {
				return nil, err
			}
			entries[dictKey(key)] = runtime.DictEntry{Key: key, Value: value}
		}
		return runtime.Dict{Entries: entries}, nil
	case *ast.SetLiteral:
		entries := map[string]runtime.SetEntry{}
		for _, element := range expr.Elements {
			value, err := decoratorMetadataValue(element)
			if err != nil {
				return nil, err
			}
			entries[dictKey(value)] = runtime.SetEntry{Value: value}
		}
		return runtime.Set{Elements: entries}, nil
	default:
		return nil, fmt.Errorf("unsupported decorator argument expression %s", expr.String())
	}
}

func netJoinHostPort(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	host, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s host must be string", call.Callee.String())
	}
	port, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s port must be string", call.Callee.String())
	}
	return runtime.String{Value: net.JoinHostPort(host.Value, port.Value)}, nil
}

func netSplitHostPort(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	addr, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "host", runtime.String{Value: host})
	putDict(entries, "port", runtime.String{Value: port})
	return runtime.Dict{Entries: entries}, nil
}

func netLookupHost(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	host, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, err
	}
	values := make([]runtime.Value, 0, len(addrs))
	for _, addr := range addrs {
		values = append(values, runtime.String{Value: addr})
	}
	return &runtime.List{Elements: values}, nil
}

func (e *Evaluator) netListenTCP(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	addr, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return e.registerNetHandle(&netHandle{listener: listener}), nil
}

func (e *Evaluator) netConnectTCP(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	addr, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return e.registerNetHandle(&netHandle{conn: conn}), nil
}

func (e *Evaluator) netAccept(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects listener", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	if handle.listener == nil {
		return nil, fmt.Errorf("%s handle is not a listener", call.Callee.String())
	}
	conn, err := handle.listener.Accept()
	if err != nil {
		return nil, err
	}
	return e.registerNetHandle(&netHandle{conn: conn}), nil
}

func (e *Evaluator) netRead(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects connection and byte count", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	if handle.conn == nil {
		return nil, fmt.Errorf("%s handle is not a connection", call.Callee.String())
	}
	size, err := int64Argument(call, args[1], "byte count")
	if err != nil {
		return nil, err
	}
	if size < 0 || size > 1<<30 {
		return nil, fmt.Errorf("%s byte count out of range", call.Callee.String())
	}
	buf := make([]byte, size)
	read, err := handle.conn.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return runtime.Bytes{Value: buf[:read]}, nil
}

func (e *Evaluator) netWrite(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects connection and data", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	if handle.conn == nil {
		return nil, fmt.Errorf("%s handle is not a connection", call.Callee.String())
	}
	data, err := bytesFromStringOrBytes(call, args[1], "data")
	if err != nil {
		return nil, err
	}
	written, err := handle.conn.Write(data)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(written)), nil
}

func (e *Evaluator) netSetDeadline(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects handle and timeout milliseconds", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	timeoutMs, err := int64Argument(call, args[1], "timeout milliseconds")
	if err != nil {
		return nil, err
	}
	if timeoutMs < 0 {
		return nil, fmt.Errorf("%s timeout milliseconds must be >= 0", call.Callee.String())
	}
	return runtime.Null{}, setNetHandleDeadline(handle, time.Now().Add(time.Duration(timeoutMs)*time.Millisecond))
}

func (e *Evaluator) netClearDeadline(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects handle", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	return runtime.Null{}, setNetHandleDeadline(handle, time.Time{})
}

func (e *Evaluator) netClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects handle", call.Callee.String())
	}
	id, err := int64Argument(call, args[0], "handle")
	if err != nil {
		return nil, err
	}
	e.netMu.Lock()
	handle, ok := e.netHandles[id]
	if ok {
		delete(e.netHandles, id)
	}
	e.netMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown net handle %d", id)
	}
	if handle.listener != nil {
		return runtime.Null{}, handle.listener.Close()
	}
	if handle.conn != nil {
		return runtime.Null{}, handle.conn.Close()
	}
	if handle.packet != nil {
		return runtime.Null{}, handle.packet.Close()
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) netLocalAddr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects handle", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	switch {
	case handle.listener != nil:
		return runtime.String{Value: handle.listener.Addr().String()}, nil
	case handle.conn != nil:
		return runtime.String{Value: handle.conn.LocalAddr().String()}, nil
	case handle.packet != nil:
		return runtime.String{Value: handle.packet.LocalAddr().String()}, nil
	default:
		return runtime.String{Value: ""}, nil
	}
}

func (e *Evaluator) netRemoteAddr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects connection", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	if handle.conn == nil {
		return nil, fmt.Errorf("%s handle is not a connection", call.Callee.String())
	}
	return runtime.String{Value: handle.conn.RemoteAddr().String()}, nil
}

func (e *Evaluator) netListenUDP(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	addr, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	packet, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	return e.registerNetHandle(&netHandle{packet: packet}), nil
}

func (e *Evaluator) netDialUDP(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	addr, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	return e.registerNetHandle(&netHandle{conn: conn}), nil
}

func (e *Evaluator) netReadFrom(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects packet socket and byte count", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	if handle.packet == nil {
		return nil, fmt.Errorf("%s handle is not a packet socket", call.Callee.String())
	}
	size, err := int64Argument(call, args[1], "byte count")
	if err != nil {
		return nil, err
	}
	if size < 0 || size > 1<<30 {
		return nil, fmt.Errorf("%s byte count out of range", call.Callee.String())
	}
	buf := make([]byte, size)
	read, addr, err := handle.packet.ReadFrom(buf)
	if err != nil {
		return nil, err
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "addr", runtime.String{Value: addr.String()})
	putDict(entries, "data", runtime.Bytes{Value: buf[:read]})
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) netWriteTo(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects packet socket, address, and data", call.Callee.String())
	}
	handle, err := e.netHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	if handle.packet == nil {
		return nil, fmt.Errorf("%s handle is not a packet socket", call.Callee.String())
	}
	addr, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s address must be string", call.Callee.String())
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr.Value)
	if err != nil {
		return nil, err
	}
	data, err := bytesFromStringOrBytes(call, args[2], "data")
	if err != nil {
		return nil, err
	}
	written, err := handle.packet.WriteTo(data, udpAddr)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(written)), nil
}

func (e *Evaluator) registerNetHandle(handle *netHandle) runtime.Value {
	e.netMu.Lock()
	defer e.netMu.Unlock()
	e.nextNetID++
	e.netHandles[e.nextNetID] = handle
	return runtime.NewInt64(e.nextNetID)
}

func (e *Evaluator) netHandle(value runtime.Value) (*netHandle, error) {
	id, err := rawInt64(value, "handle")
	if err != nil {
		return nil, err
	}
	e.netMu.Lock()
	handle, ok := e.netHandles[id]
	e.netMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.netHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown net handle %d", id)
	}
	return handle, nil
}

// netDial opens a TCP (or TLS) connection and returns a dict with
// the connection handle, an IOStream wrapping the conn, and local /
// remote address strings. Mirrors the F4 proc.spawn shape so the
// stdlib `net.Socket` class can hand back ready-made IOStream values
// for read / write / readLine / lines / close.
//
// Args: (host: string, port: int, opts: dict<string, any> = {})
// Recognised opts: "tls" (bool) for TLS dial, "timeoutMs" (int) for
// connect timeout.
func (e *Evaluator) netDial(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects (host, port, opts?)", call.Callee.String())
	}
	host, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s host must be string", call.Callee.String())
	}
	port, ok := native.AsInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s port must be int", call.Callee.String())
	}
	useTLS := false
	var timeoutMs int64
	if len(args) == 3 {
		opts, ok := args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s options must be a dict", call.Callee.String())
		}
		if v, found := dictField(opts, "tls"); found {
			b, ok := v.(runtime.Bool)
			if !ok {
				return nil, fmt.Errorf("%s options.tls must be bool", call.Callee.String())
			}
			useTLS = b.Value
		}
		if v, found := dictField(opts, "timeoutMs"); found {
			n, ok := native.AsInt64(v)
			if !ok {
				return nil, fmt.Errorf("%s options.timeoutMs must be int", call.Callee.String())
			}
			timeoutMs = n
		}
	}
	addr := net.JoinHostPort(host.Value, strconv.FormatInt(port, 10))
	var conn net.Conn
	var err error
	dialer := &net.Dialer{}
	if timeoutMs > 0 {
		dialer.Timeout = time.Duration(timeoutMs) * time.Millisecond
	}
	if useTLS {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: host.Value}}
		conn, err = tlsDialer.Dial("tcp", addr)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	netHandleValue := e.registerNetHandle(&netHandle{conn: conn})
	streamHandle := &ioStreamHandle{name: "net socket", reader: conn, writer: conn, closer: conn}
	streamValue := e.registerIOStream(streamHandle)
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "handle", netHandleValue)
	putDict(entries, "stream", streamValue)
	putDict(entries, "localAddr", runtime.String{Value: conn.LocalAddr().String()})
	putDict(entries, "remoteAddr", runtime.String{Value: conn.RemoteAddr().String()})
	return runtime.Dict{Entries: entries}, nil
}

// netServe binds a listener, spawns an accept-loop goroutine, and
// dispatches each accepted connection to the user's handler callback
// as a Socket-shaped dict. The handler runs in a child evaluator so
// captured module-level state is observable; the wrap-bridge route
// is used on the VM side. Returns a server handle for shutdown.
func (e *Evaluator) netServe(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 3 || len(args) > 4 {
		return nil, fmt.Errorf("%s expects (host, port, handler, opts?)", call.Callee.String())
	}
	host, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s host must be string", call.Callee.String())
	}
	port, ok := native.AsInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s port must be int", call.Callee.String())
	}
	handler, ok := args[2].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("%s handler must be a function", call.Callee.String())
	}
	pool, err := serverPoolFromArgs(args, 3, call.Callee.String())
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(host.Value, strconv.FormatInt(port, 10))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := &netServerHandle{listener: listener, pool: pool}
	e.netServerMu.Lock()
	e.nextNetServerID++
	id := e.nextNetServerID
	e.netServers[id] = server
	e.netServerMu.Unlock()
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handlerConn := conn
			if pool != nil && !pool.IsUnbounded() {
				if err := pool.Acquire(context.Background()); err != nil {
					_ = handlerConn.Close()
					continue
				}
			}
			server.wg.Add(1)
			go func() {
				defer server.wg.Done()
				if pool != nil && !pool.IsUnbounded() {
					defer pool.Release()
				}
				child := e.childForCallback()
				defer child.Cleanup()
				connHandle := e.registerNetHandle(&netHandle{conn: handlerConn})
				streamHandle := &ioStreamHandle{name: "net socket", reader: handlerConn, writer: handlerConn, closer: handlerConn}
				streamValue := e.registerIOStream(streamHandle)
				entries := map[string]runtime.DictEntry{}
				putDict(entries, "handle", connHandle)
				putDict(entries, "stream", streamValue)
				putDict(entries, "localAddr", runtime.String{Value: handlerConn.LocalAddr().String()})
				putDict(entries, "remoteAddr", runtime.String{Value: handlerConn.RemoteAddr().String()})
				socketDict := runtime.Dict{Entries: entries}
				_, _ = child.applyFunction(handler, []runtime.Value{socketDict})
			}()
		}
	}()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "handle", runtime.NewInt64(id))
	putDict(entries, "localAddr", runtime.String{Value: listener.Addr().String()})
	return runtime.Dict{Entries: entries}, nil
}

// netCloseListener stops an accept-loop and joins the goroutine so
// callers can rely on reads of callback-touched state to happen
// after the last handler invocation.
func (e *Evaluator) netCloseListener(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects (server handle)", call.Callee.String())
	}
	id, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s server handle must be int", call.Callee.String())
	}
	e.netServerMu.Lock()
	server, ok := e.netServers[id]
	if ok {
		delete(e.netServers, id)
	}
	e.netServerMu.Unlock()
	if !ok {
		if e.parent != nil {
			return e.parent.netCloseListener(call, args)
		}
		return runtime.Null{}, nil
	}
	return runtime.Null{}, stopNetServerHandle(server)
}

func stopNetServerHandle(server *netServerHandle) error {
	if server == nil || server.stopped {
		return nil
	}
	server.stopped = true
	err := server.listener.Close()
	server.wg.Wait()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func setNetHandleDeadline(handle *netHandle, deadline time.Time) error {
	type deadliner interface {
		SetDeadline(time.Time) error
	}
	switch {
	case handle.listener != nil:
		deadlineListener, ok := handle.listener.(deadliner)
		if !ok {
			return fmt.Errorf("listener does not support deadlines")
		}
		return deadlineListener.SetDeadline(deadline)
	case handle.conn != nil:
		return handle.conn.SetDeadline(deadline)
	case handle.packet != nil:
		return handle.packet.SetDeadline(deadline)
	default:
		return nil
	}
}

func int64Argument(call *ast.CallExpression, value runtime.Value, name string) (int64, error) {
	n, err := rawInt64(value, name)
	if err != nil {
		return 0, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	return n, nil
}

func rawInt64(value runtime.Value, name string) (int64, error) {
	n, ok := native.AsInt64(value)
	if !ok {
		return 0, fmt.Errorf("%s must be int", name)
	}
	return n, nil
}

func bytesFromStringOrBytes(call *ast.CallExpression, value runtime.Value, name string) ([]byte, error) {
	switch value := value.(type) {
	case runtime.String:
		return []byte(value.Value), nil
	case runtime.Bytes:
		return value.Value, nil
	default:
		return nil, fmt.Errorf("%s %s must be string or bytes", call.Callee.String(), name)
	}
}

func (e *Evaluator) dbConnectionObject(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if e.dbConnectionClass == nil {
		if err := e.installBuiltinTypes(runtime.NewEnvironment()); err != nil {
			return nil, err
		}
	}
	return e.instantiateClass(e.dbConnectionClass, args)
}

func (e *Evaluator) dbOpen(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	driverName, dsn, err := dbConnectionSpec(call, args)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	e.dbMu.Lock()
	defer e.dbMu.Unlock()
	e.nextDBID++
	e.dbs[e.nextDBID] = db
	e.dbDrivers[e.nextDBID] = driverName
	return runtime.NewInt64(e.nextDBID), nil
}

func (e *Evaluator) dbExec(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects at least two arguments", call.Callee.String())
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	query, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s query must be string", call.Callee.String())
	}
	sqlArgs, err := dbArgs(args[2:])
	if err != nil {
		return nil, err
	}
	result, err := db.Exec(query.Value, sqlArgs...)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "rowsAffected", runtime.NewInt64(rows))
	putDict(entries, "lastInsertId", runtime.NewInt64(lastID))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbExecStandard(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects database handle and query", call.Callee.String())
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	driver, err := e.dbDriverFromValue(args[0])
	if err != nil {
		return nil, err
	}
	query, sqlArgs, err := dbStandardQueryAndArgs(call, driver, args[1:])
	if err != nil {
		return nil, err
	}
	result, err := db.Exec(query, sqlArgs...)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "rowsAffected", runtime.NewInt64(rows))
	putDict(entries, "lastInsertId", runtime.NewInt64(lastID))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbQuery(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects at least two arguments", call.Callee.String())
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	query, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s query must be string", call.Callee.String())
	}
	sqlArgs, err := dbArgs(args[2:])
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(query.Value, sqlArgs...)
	if err != nil {
		return nil, err
	}
	return sqlRowsToRuntime(rows)
}

func (e *Evaluator) dbQueryRows(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects at least two arguments", call.Callee.String())
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	driver, err := e.dbDriverFromValue(args[0])
	if err != nil {
		return nil, err
	}
	query, sqlArgs, err := dbStandardQueryAndArgs(call, driver, args[1:])
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(query, sqlArgs...)
	if err != nil {
		return nil, err
	}
	return e.registerDBRows(rows)
}

func (e *Evaluator) dbBegin(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	driver, err := e.dbDriverFromValue(args[0])
	if err != nil {
		return nil, err
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	defer e.dbMu.Unlock()
	e.nextTxID++
	e.txs[e.nextTxID] = &dbTxHandle{tx: tx, driver: driver}
	return runtime.NewInt64(e.nextTxID), nil
}

func (e *Evaluator) dbTxExec(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects at least two arguments", call.Callee.String())
	}
	tx, err := e.txHandle(args[0])
	if err != nil {
		return nil, err
	}
	query, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s query must be string", call.Callee.String())
	}
	sqlArgs, err := dbArgs(args[2:])
	if err != nil {
		return nil, err
	}
	result, err := tx.tx.Exec(query.Value, sqlArgs...)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "rowsAffected", runtime.NewInt64(rows))
	putDict(entries, "lastInsertId", runtime.NewInt64(lastID))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbTxExecStandard(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects transaction handle and query", call.Callee.String())
	}
	tx, err := e.txHandle(args[0])
	if err != nil {
		return nil, err
	}
	query, sqlArgs, err := dbStandardQueryAndArgs(call, tx.driver, args[1:])
	if err != nil {
		return nil, err
	}
	result, err := tx.tx.Exec(query, sqlArgs...)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "rowsAffected", runtime.NewInt64(rows))
	putDict(entries, "lastInsertId", runtime.NewInt64(lastID))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbTxQuery(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects at least two arguments", call.Callee.String())
	}
	tx, err := e.txHandle(args[0])
	if err != nil {
		return nil, err
	}
	query, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s query must be string", call.Callee.String())
	}
	sqlArgs, err := dbArgs(args[2:])
	if err != nil {
		return nil, err
	}
	rows, err := tx.tx.Query(query.Value, sqlArgs...)
	if err != nil {
		return nil, err
	}
	return sqlRowsToRuntime(rows)
}

func (e *Evaluator) dbTxQueryRows(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s expects at least two arguments", call.Callee.String())
	}
	tx, err := e.txHandle(args[0])
	if err != nil {
		return nil, err
	}
	query, sqlArgs, err := dbStandardQueryAndArgs(call, tx.driver, args[1:])
	if err != nil {
		return nil, err
	}
	rows, err := tx.tx.Query(query, sqlArgs...)
	if err != nil {
		return nil, err
	}
	return e.registerDBRows(rows)
}

func (e *Evaluator) dbCommit(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	tx, err := e.takeTxHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, tx.tx.Commit()
}

func (e *Evaluator) dbRollback(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	tx, err := e.takeTxHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, tx.tx.Rollback()
}

func (e *Evaluator) dbPrepare(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	driver, err := e.dbDriverFromValue(args[0])
	if err != nil {
		return nil, err
	}
	query, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s query must be string", call.Callee.String())
	}
	preparedQuery, paramNames, err := dbNormalizeQuery(query.Value, driver)
	if err != nil {
		return nil, err
	}
	stmt, err := db.Prepare(preparedQuery)
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	defer e.dbMu.Unlock()
	e.nextStmtID++
	e.stmts[e.nextStmtID] = &dbStmtHandle{stmt: stmt, driver: driver, paramNames: paramNames}
	return runtime.NewInt64(e.nextStmtID), nil
}

func (e *Evaluator) dbStmtExec(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s expects at least one argument", call.Callee.String())
	}
	stmt, err := e.stmtHandle(args[0])
	if err != nil {
		return nil, err
	}
	sqlArgs, err := dbArgs(args[1:])
	if err != nil {
		return nil, err
	}
	result, err := stmt.stmt.Exec(sqlArgs...)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "rowsAffected", runtime.NewInt64(rows))
	putDict(entries, "lastInsertId", runtime.NewInt64(lastID))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbStmtExecStandard(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s expects statement handle", call.Callee.String())
	}
	stmt, err := e.stmtHandle(args[0])
	if err != nil {
		return nil, err
	}
	sqlArgs, err := dbPreparedArgs(args[1:], stmt.paramNames)
	if err != nil {
		return nil, err
	}
	result, err := stmt.stmt.Exec(sqlArgs...)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "rowsAffected", runtime.NewInt64(rows))
	putDict(entries, "lastInsertId", runtime.NewInt64(lastID))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbStmtQuery(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s expects at least one argument", call.Callee.String())
	}
	stmt, err := e.stmtHandle(args[0])
	if err != nil {
		return nil, err
	}
	sqlArgs, err := dbArgs(args[1:])
	if err != nil {
		return nil, err
	}
	rows, err := stmt.stmt.Query(sqlArgs...)
	if err != nil {
		return nil, err
	}
	return sqlRowsToRuntime(rows)
}

func (e *Evaluator) dbStmtQueryRows(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s expects at least one argument", call.Callee.String())
	}
	stmt, err := e.stmtHandle(args[0])
	if err != nil {
		return nil, err
	}
	sqlArgs, err := dbPreparedArgs(args[1:], stmt.paramNames)
	if err != nil {
		return nil, err
	}
	rows, err := stmt.stmt.Query(sqlArgs...)
	if err != nil {
		return nil, err
	}
	return e.registerDBRows(rows)
}

func (e *Evaluator) dbStmtClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	stmt, err := e.takeStmtHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, stmt.stmt.Close()
}

func (e *Evaluator) dbConfigure(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects database handle and options", call.Callee.String())
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	options, ok := args[1].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s options must be dict", call.Callee.String())
	}
	if value, ok := dictField(options, "maxOpenConns"); ok {
		n, err := intOption(call, value, "maxOpenConns")
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(n)
	}
	if value, ok := dictField(options, "maxIdleConns"); ok {
		n, err := intOption(call, value, "maxIdleConns")
		if err != nil {
			return nil, err
		}
		db.SetMaxIdleConns(n)
	}
	if value, ok := dictField(options, "connMaxLifetimeMs"); ok {
		n, err := intOption(call, value, "connMaxLifetimeMs")
		if err != nil {
			return nil, err
		}
		db.SetConnMaxLifetime(time.Duration(n) * time.Millisecond)
	}
	if value, ok := dictField(options, "connMaxIdleTimeMs"); ok {
		n, err := intOption(call, value, "connMaxIdleTimeMs")
		if err != nil {
			return nil, err
		}
		db.SetConnMaxIdleTime(time.Duration(n) * time.Millisecond)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) dbStats(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	stats := db.Stats()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "maxOpenConnections", runtime.NewInt64(int64(stats.MaxOpenConnections)))
	putDict(entries, "openConnections", runtime.NewInt64(int64(stats.OpenConnections)))
	putDict(entries, "inUse", runtime.NewInt64(int64(stats.InUse)))
	putDict(entries, "idle", runtime.NewInt64(int64(stats.Idle)))
	putDict(entries, "waitCount", runtime.NewInt64(stats.WaitCount))
	putDict(entries, "waitDurationMs", runtime.NewInt64(stats.WaitDuration.Milliseconds()))
	putDict(entries, "maxIdleClosed", runtime.NewInt64(stats.MaxIdleClosed))
	putDict(entries, "maxIdleTimeClosed", runtime.NewInt64(stats.MaxIdleTimeClosed))
	putDict(entries, "maxLifetimeClosed", runtime.NewInt64(stats.MaxLifetimeClosed))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbMigrate(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects database handle and migrations", call.Callee.String())
	}
	handle, err := dbHandleID(args[0])
	if err != nil {
		return nil, err
	}
	db, err := e.dbHandle(args[0])
	if err != nil {
		return nil, err
	}
	driver, err := e.dbDriver(handle)
	if err != nil {
		return nil, err
	}
	idPlaceholder := dbPlaceholder(driver, 1)
	appliedAtPlaceholder := dbPlaceholder(driver, 2)
	migrations, ok := args[1].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s migrations must be list<dict>", call.Callee.String())
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`create table if not exists geblang_migrations (id text primary key, applied_at text not null)`); err != nil {
		return nil, err
	}
	applied := []runtime.Value{}
	skipped := []runtime.Value{}
	for _, migration := range migrations.Elements {
		dict, ok := migration.(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s each migration must be dict", call.Callee.String())
		}
		id, ok := dictStringField(dict, "id")
		if !ok || id == "" {
			return nil, fmt.Errorf("%s migration.id must be non-empty string", call.Callee.String())
		}
		sqlText, ok := dictStringField(dict, "sql")
		if !ok || strings.TrimSpace(sqlText) == "" {
			return nil, fmt.Errorf("%s migration.sql must be non-empty string", call.Callee.String())
		}
		var existing string
		err := tx.QueryRow(`select id from geblang_migrations where id = `+idPlaceholder, id).Scan(&existing)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			skipped = append(skipped, runtime.String{Value: id})
			continue
		}
		if _, err := tx.Exec(sqlText); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`insert into geblang_migrations (id, applied_at) values (`+idPlaceholder+`, `+appliedAtPlaceholder+`)`, id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		applied = append(applied, runtime.String{Value: id})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "applied", &runtime.List{Elements: applied})
	putDict(entries, "skipped", &runtime.List{Elements: skipped})
	return runtime.Dict{Entries: entries}, nil
}

func intOption(call *ast.CallExpression, value runtime.Value, name string) (int, error) {
	n, err := int64Argument(call, value, name)
	if err != nil {
		return 0, err
	}
	if n < 0 || n > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("%s %s out of range", call.Callee.String(), name)
	}
	return int(n), nil
}

func sqlRowsToRuntime(rows *sql.Rows) (runtime.Value, error) {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []runtime.Value{}
	for rows.Next() {
		raw := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		entries := map[string]runtime.DictEntry{}
		for i, column := range columns {
			value, err := sqlValueToRuntime(raw[i])
			if err != nil {
				return nil, err
			}
			putDict(entries, column, value)
		}
		out = append(out, runtime.Dict{Entries: entries})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &runtime.List{Elements: out}, nil
}

func scanSQLRow(rows *sql.Rows, columns []string) (runtime.Value, error) {
	raw := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range raw {
		dest[i] = &raw[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	entries := map[string]runtime.DictEntry{}
	for i, column := range columns {
		value, err := sqlValueToRuntime(raw[i])
		if err != nil {
			return nil, err
		}
		putDict(entries, column, value)
	}
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) dbClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	handle, err := dbHandleID(args[0])
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	db, ok := e.dbs[handle]
	if ok {
		delete(e.dbs, handle)
		delete(e.dbDrivers, handle)
	}
	e.dbMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown database handle %d", handle)
	}
	return runtime.Null{}, db.Close()
}

func (e *Evaluator) dbHandle(value runtime.Value) (*sql.DB, error) {
	handle, err := dbHandleID(value)
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	db, ok := e.dbs[handle]
	e.dbMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.dbHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown database handle %d", handle)
	}
	return db, nil
}

func (e *Evaluator) dbDriver(handle int64) (string, error) {
	e.dbMu.Lock()
	driver, ok := e.dbDrivers[handle]
	e.dbMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.dbDriver(handle)
	}
	if !ok {
		return "", fmt.Errorf("unknown database handle %d", handle)
	}
	return driver, nil
}

func (e *Evaluator) dbDriverFromValue(value runtime.Value) (string, error) {
	handle, err := dbHandleID(value)
	if err != nil {
		return "", err
	}
	return e.dbDriver(handle)
}

func (e *Evaluator) txHandle(value runtime.Value) (*dbTxHandle, error) {
	handle, err := dbHandleID(value)
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	tx, ok := e.txs[handle]
	e.dbMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.txHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown transaction handle %d", handle)
	}
	return tx, nil
}

func (e *Evaluator) takeTxHandle(value runtime.Value) (*dbTxHandle, error) {
	handle, err := dbHandleID(value)
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	tx, ok := e.txs[handle]
	if ok {
		delete(e.txs, handle)
	}
	e.dbMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.takeTxHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown transaction handle %d", handle)
	}
	return tx, nil
}

func (e *Evaluator) stmtHandle(value runtime.Value) (*dbStmtHandle, error) {
	handle, err := dbHandleID(value)
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	stmt, ok := e.stmts[handle]
	e.dbMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.stmtHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown statement handle %d", handle)
	}
	return stmt, nil
}

func (e *Evaluator) takeStmtHandle(value runtime.Value) (*dbStmtHandle, error) {
	handle, err := dbHandleID(value)
	if err != nil {
		return nil, err
	}
	e.dbMu.Lock()
	stmt, ok := e.stmts[handle]
	if ok {
		delete(e.stmts, handle)
	}
	e.dbMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.takeStmtHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown statement handle %d", handle)
	}
	return stmt, nil
}

func dbHandleID(value runtime.Value) (int64, error) {
	id, ok := value.(runtime.Int)
	if !ok || !id.Value.IsInt64() {
		return 0, fmt.Errorf("database handle must be int")
	}
	return id.Value.Int64(), nil
}

func dbConnectionSpec(call *ast.CallExpression, args []runtime.Value) (string, string, error) {
	if len(args) == 1 {
		options, ok := args[0].(runtime.Dict)
		if !ok {
			return "", "", fmt.Errorf("%s expects options dict or driver and connection string", call.Callee.String())
		}
		driver, ok := dictStringField(options, "driver")
		if !ok || driver == "" {
			return "", "", fmt.Errorf("%s options.driver must be a non-empty string", call.Callee.String())
		}
		driverName, err := dbDriverName(driver)
		if err != nil {
			return "", "", err
		}
		if dsn, ok := firstDictStringField(options, "dsn", "connectionString", "url"); ok {
			return driverName, dsn, nil
		}
		dsn, err := dbBuildDSN(driver, options)
		if err != nil {
			return "", "", fmt.Errorf("%s %v", call.Callee.String(), err)
		}
		return driverName, dsn, nil
	}
	if len(args) != 2 {
		return "", "", fmt.Errorf("%s expects options dict or driver and connection string", call.Callee.String())
	}
	driver, ok := args[0].(runtime.String)
	if !ok {
		return "", "", fmt.Errorf("%s driver must be string", call.Callee.String())
	}
	dsn, ok := args[1].(runtime.String)
	if !ok {
		return "", "", fmt.Errorf("%s connection string must be string", call.Callee.String())
	}
	driverName, err := dbDriverName(driver.Value)
	if err != nil {
		return "", "", err
	}
	return driverName, dsn.Value, nil
}

func dbDriverName(name string) (string, error) {
	switch name {
	case "sqlite":
		return "sqlite", nil
	case "postgres":
		return "pgx", nil
	case "mysql":
		return "mysql", nil
	default:
		return "", fmt.Errorf("unsupported database driver %q", name)
	}
}

func dbBuildDSN(driver string, options runtime.Dict) (string, error) {
	switch driver {
	case "sqlite":
		if path, ok := firstDictStringField(options, "path", "file", "database", "dbname"); ok && path != "" {
			return path, nil
		}
		if memory, ok := dictBoolField(options, "memory"); ok && memory {
			return ":memory:", nil
		}
		return "", fmt.Errorf("sqlite options require path or memory")
	case "postgres":
		host, _ := dictStringField(options, "host")
		if host == "" {
			host = "localhost"
		}
		port := int64(5432)
		if value, ok := dictField(options, "port"); ok {
			n, err := runtimeInt64(value, "port")
			if err != nil {
				return "", err
			}
			port = n
		}
		database, _ := firstDictStringField(options, "database", "dbname")
		user, _ := dictStringField(options, "user")
		password, _ := dictStringField(options, "password")
		sslmode, _ := dictStringField(options, "sslmode")
		if sslmode == "" {
			sslmode = "disable"
		}
		parts := []string{fmt.Sprintf("host=%s", host), fmt.Sprintf("port=%d", port)}
		if user != "" {
			parts = append(parts, fmt.Sprintf("user=%s", user))
		}
		if password != "" {
			parts = append(parts, fmt.Sprintf("password=%s", password))
		}
		if database != "" {
			parts = append(parts, fmt.Sprintf("dbname=%s", database))
		}
		parts = append(parts, fmt.Sprintf("sslmode=%s", sslmode))
		return strings.Join(parts, " "), nil
	case "mysql":
		user, _ := dictStringField(options, "user")
		password, _ := dictStringField(options, "password")
		database, _ := firstDictStringField(options, "database", "dbname")
		protocol := "tcp"
		host, _ := dictStringField(options, "host")
		socket, _ := dictStringField(options, "socket")
		if socket != "" {
			protocol = "unix"
			host = socket
		} else {
			if host == "" {
				host = "127.0.0.1"
			}
			port := int64(3306)
			if value, ok := dictField(options, "port"); ok {
				n, err := runtimeInt64(value, "port")
				if err != nil {
					return "", err
				}
				port = n
			}
			host = fmt.Sprintf("%s:%d", host, port)
		}
		auth := user
		if password != "" {
			auth += ":" + password
		}
		query := []string{}
		if parseTime, ok := dictBoolField(options, "parseTime"); ok {
			query = append(query, "parseTime="+strconv.FormatBool(parseTime))
		} else {
			query = append(query, "parseTime=true")
		}
		if charset, ok := dictStringField(options, "charset"); ok && charset != "" {
			query = append(query, "charset="+neturl.QueryEscape(charset))
		}
		if loc, ok := dictStringField(options, "loc"); ok && loc != "" {
			query = append(query, "loc="+neturl.QueryEscape(loc))
		}
		return fmt.Sprintf("%s@%s(%s)/%s?%s", auth, protocol, host, database, strings.Join(query, "&")), nil
	default:
		return "", fmt.Errorf("unsupported database driver %q", driver)
	}
}

func dbPlaceholder(driver string, index int) string {
	if driver == "pgx" {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func dbStandardQueryAndArgs(call *ast.CallExpression, driver string, args []runtime.Value) (string, []any, error) {
	if len(args) < 1 {
		return "", nil, fmt.Errorf("%s expects query", call.Callee.String())
	}
	query, ok := args[0].(runtime.String)
	if !ok {
		return "", nil, fmt.Errorf("%s query must be string", call.Callee.String())
	}
	normalized, paramNames, err := dbNormalizeQuery(query.Value, driver)
	if err != nil {
		return "", nil, err
	}
	sqlArgs, err := dbBindArgs(args[1:], paramNames)
	if err != nil {
		return "", nil, fmt.Errorf("%s %v", call.Callee.String(), err)
	}
	return normalized, sqlArgs, nil
}

func dbNormalizeQuery(query, driver string) (string, []string, error) {
	var out strings.Builder
	names := []string{}
	index := 1
	for i := 0; i < len(query); {
		ch := query[i]
		if ch == '\'' || ch == '"' || ch == '`' {
			next := copyQuotedSQL(&out, query, i, ch)
			i = next
			continue
		}
		if ch == '-' && i+1 < len(query) && query[i+1] == '-' {
			next := copySQLUntilNewline(&out, query, i)
			i = next
			continue
		}
		if ch == '/' && i+1 < len(query) && query[i+1] == '*' {
			next := copySQLBlockComment(&out, query, i)
			i = next
			continue
		}
		if ch == '?' {
			out.WriteString(dbPlaceholder(driver, index))
			index++
			i++
			continue
		}
		if ch == ':' && i+1 < len(query) && query[i+1] != ':' && isDBParamStart(query[i+1]) {
			j := i + 2
			for j < len(query) && isDBParamPart(query[j]) {
				j++
			}
			names = append(names, query[i+1:j])
			out.WriteString(dbPlaceholder(driver, index))
			index++
			i = j
			continue
		}
		out.WriteByte(ch)
		i++
	}
	return out.String(), names, nil
}

func copyQuotedSQL(out *strings.Builder, query string, start int, quote byte) int {
	out.WriteByte(query[start])
	for i := start + 1; i < len(query); i++ {
		out.WriteByte(query[i])
		if query[i] == quote {
			if i+1 < len(query) && query[i+1] == quote {
				i++
				out.WriteByte(query[i])
				continue
			}
			return i + 1
		}
		if query[i] == '\\' && i+1 < len(query) {
			i++
			out.WriteByte(query[i])
		}
	}
	return len(query)
}

func copySQLUntilNewline(out *strings.Builder, query string, start int) int {
	for i := start; i < len(query); i++ {
		out.WriteByte(query[i])
		if query[i] == '\n' {
			return i + 1
		}
	}
	return len(query)
}

func copySQLBlockComment(out *strings.Builder, query string, start int) int {
	out.WriteString("/*")
	for i := start + 2; i < len(query); i++ {
		out.WriteByte(query[i])
		if query[i] == '*' && i+1 < len(query) && query[i+1] == '/' {
			out.WriteByte('/')
			return i + 2
		}
	}
	return len(query)
}

func isDBParamStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isDBParamPart(ch byte) bool {
	return isDBParamStart(ch) || (ch >= '0' && ch <= '9')
}

func dbBindArgs(values []runtime.Value, paramNames []string) ([]any, error) {
	if len(values) == 1 {
		switch value := values[0].(type) {
		case *runtime.List:
			return dbArgs(value.Elements)
		case runtime.Dict:
			if len(paramNames) == 0 {
				return nil, fmt.Errorf("named parameter dict requires :name placeholders")
			}
			return dbNamedArgs(value, paramNames)
		}
	}
	return dbArgs(values)
}

func dbPreparedArgs(values []runtime.Value, paramNames []string) ([]any, error) {
	return dbBindArgs(values, paramNames)
}

func dbNamedArgs(dict runtime.Dict, names []string) ([]any, error) {
	args := make([]any, 0, len(names))
	for _, name := range names {
		value, ok := dictField(dict, name)
		if !ok {
			return nil, fmt.Errorf("missing named database parameter %q", name)
		}
		arg, err := dbArg(value)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}

func dbArgs(values []runtime.Value) ([]any, error) {
	args := make([]any, 0, len(values))
	for _, value := range values {
		arg, err := dbArg(value)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}

func dbArg(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Null:
		return nil, nil
	case runtime.Bool:
		return value.Value, nil
	case runtime.Int:
		if !value.Value.IsInt64() {
			return nil, fmt.Errorf("database int argument is out of int64 range")
		}
		return value.Value.Int64(), nil
	case runtime.Decimal:
		return value.Value.FloatString(10), nil
	case runtime.Float:
		return value.Value, nil
	case runtime.String:
		return value.Value, nil
	default:
		return nil, fmt.Errorf("unsupported database argument type %s", value.TypeName())
	}
}

func sqlValueToRuntime(value any) (runtime.Value, error) {
	switch value := value.(type) {
	case nil:
		return runtime.Null{}, nil
	case bool:
		return runtime.Bool{Value: value}, nil
	case int64:
		return runtime.NewInt64(value), nil
	case float64:
		return runtime.Float{Value: value}, nil
	case string:
		return runtime.String{Value: value}, nil
	case []byte:
		return runtime.Bytes{Value: value}, nil
	case time.Time:
		return runtime.String{Value: value.UTC().Format(time.RFC3339Nano)}, nil
	default:
		return nil, fmt.Errorf("unsupported database value type %T", value)
	}
}

func putDict(entries map[string]runtime.DictEntry, key string, value runtime.Value) {
	keyValue := runtime.String{Value: key}
	entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
}

func dictField(dict runtime.Dict, key string) (runtime.Value, bool) {
	entry, ok := dict.Entries[dictKey(runtime.String{Value: key})]
	if ok {
		return entry.Value, true
	}
	for _, entry := range dict.Entries {
		stringKey, ok := entry.Key.(runtime.String)
		if ok && stringKey.Value == key {
			return entry.Value, true
		}
	}
	return nil, false
}

func dictStringField(dict runtime.Dict, key string) (string, bool) {
	value, ok := dictField(dict, key)
	if !ok {
		return "", false
	}
	text, ok := value.(runtime.String)
	if !ok {
		return "", false
	}
	return text.Value, true
}

func firstDictStringField(dict runtime.Dict, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := dictStringField(dict, key); ok {
			return value, true
		}
	}
	return "", false
}

func dictBoolField(dict runtime.Dict, key string) (bool, bool) {
	value, ok := dictField(dict, key)
	if !ok {
		return false, false
	}
	boolValue, ok := value.(runtime.Bool)
	if !ok {
		return false, false
	}
	return boolValue.Value, true
}

func runtimeInt64(value runtime.Value, name string) (int64, error) {
	intValue, ok := value.(runtime.Int)
	if !ok || !intValue.Value.IsInt64() {
		return 0, fmt.Errorf("%s must be int", name)
	}
	return intValue.Value.Int64(), nil
}

// httpBodyReader converts a Geblang body value into an io.Reader the
// HTTP client can consume. Strings and bytes use in-memory readers
// (Content-Length is set automatically by net/http). An IOStream
// hands back its underlying reader, which produces chunked
// transfer-encoding for unknown-length bodies. Returns (nil, nil) for
// runtime.Null so callers can build requests without a body.
func (e *Evaluator) httpBodyReader(value runtime.Value, label string) (io.Reader, error) {
	switch v := value.(type) {
	case nil, runtime.Null:
		return nil, nil
	case runtime.String:
		return strings.NewReader(v.Value), nil
	case runtime.Bytes:
		return bytes.NewReader(v.Value), nil
	case runtime.NativeObject:
		if v.Kind == "IOStream" || v.Kind == "IOCapture" {
			handle, err := e.ioStreamHandle(v)
			if err != nil {
				return nil, err
			}
			if handle.reader == nil {
				return nil, fmt.Errorf("%s body stream is not readable", label)
			}
			if handle.bufReader != nil {
				return handle.bufReader, nil
			}
			return handle.reader, nil
		}
	case runtime.Int, runtime.SmallInt:
		// File handle from io.open. Read straight from the underlying
		// file - the HTTP client closes its own copy of the reader
		// when the request finishes, but the file handle stays in
		// e.files until the user calls io.close.
		file, err := e.fileHandle(value)
		if err != nil {
			return nil, fmt.Errorf("%s %w", label, err)
		}
		return file, nil
	case *runtime.Instance:
		// streams.IOStream (and subclasses) store the native handle
		// in a `handle` field; recurse on that so the caller can
		// pass the friendly OO wrapper directly.
		if v != nil && v.Class != nil {
			if inner, ok := v.Fields["handle"]; ok {
				return e.httpBodyReader(inner, label)
			}
		}
	}
	return nil, fmt.Errorf("%s body must be string, bytes, or IOStream (got %T)", label, value)
}

func (e *Evaluator) httpRequest(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 && len(args) != 4 {
		return nil, fmt.Errorf("%s expects three or four arguments", call.Callee.String())
	}
	method, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s method must be string", call.Callee.String())
	}
	url, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s URL must be string", call.Callee.String())
	}
	bodyReader, err := e.httpBodyReader(args[2], call.Callee.String())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method.Value, url.Value, bodyReader)
	if err != nil {
		return nil, err
	}
	if len(args) == 4 {
		if err := applyRequestHeaders(call, req, args[3]); err != nil {
			return nil, err
		}
	}
	applyDefaultUserAgent(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	headers := map[string]runtime.DictEntry{}
	for name, values := range resp.Header {
		key := runtime.String{Value: name}
		headers[dictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.String{Value: strings.Join(values, ",")}}
	}
	entries := map[string]runtime.DictEntry{}
	put := func(key string, value runtime.Value) {
		keyValue := runtime.String{Value: key}
		entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
	}
	put("status", runtime.NewInt64(int64(resp.StatusCode)))
	put("body", runtime.String{Value: string(responseBody)})
	put("headers", runtime.Dict{Entries: headers})
	return runtime.Dict{Entries: entries}, nil
}

func httpGet(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects url and optional headers", call.Callee.String())
	}
	url, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s URL must be string", call.Callee.String())
	}
	req, err := http.NewRequest(http.MethodGet, url.Value, nil)
	if err != nil {
		return nil, err
	}
	if len(args) == 2 {
		if err := applyRequestHeaders(call, req, args[1]); err != nil {
			return nil, err
		}
	}
	return doHTTPRequest(http.DefaultClient, req)
}

func (e *Evaluator) httpPost(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects url, body, and optional headers", call.Callee.String())
	}
	url, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s URL must be string", call.Callee.String())
	}
	bodyReader, err := e.httpBodyReader(args[1], call.Callee.String())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url.Value, bodyReader)
	if err != nil {
		return nil, err
	}
	if len(args) == 3 {
		if err := applyRequestHeaders(call, req, args[2]); err != nil {
			return nil, err
		}
	}
	return doHTTPRequest(http.DefaultClient, req)
}

func httpPostJSON(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects url, value, and optional headers", call.Callee.String())
	}
	url, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s URL must be string", call.Callee.String())
	}
	encoded, err := valueToJSON(args[1])
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(encoded); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url.Value, strings.NewReader(strings.TrimSuffix(body.String(), "\n")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if len(args) == 3 {
		if err := applyRequestHeaders(call, req, args[2]); err != nil {
			return nil, err
		}
	}
	return doHTTPRequest(http.DefaultClient, req)
}

func httpParseJSON(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects response dict", call.Callee.String())
	}
	response, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s response must be dict", call.Callee.String())
	}
	body, ok := dictStringField(response, "body")
	if !ok {
		return nil, fmt.Errorf("%s response.body must be string", call.Callee.String())
	}
	value, parseErr := native.ParseJSONText(body)
	if parseErr != nil {
		return nil, fmt.Errorf("%s", parseErr.Message)
	}
	return value, nil
}

func applyRequestHeaders(call *ast.CallExpression, req *http.Request, value runtime.Value) error {
	headers, ok := httpHeaderValue(value)
	if !ok {
		return fmt.Errorf("%s headers must be dict or http.Headers", call.Callee.String())
	}
	for key, values := range headers.Values {
		for i, value := range values {
			if i == 0 {
				req.Header.Set(key, value)
			} else {
				req.Header.Add(key, value)
			}
		}
	}
	return nil
}

func (e *Evaluator) httpRequestWithOptions(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	options, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s options must be dict", call.Callee.String())
	}
	method := "GET"
	if value, ok := dictStringField(options, "method"); ok {
		method = value
	}
	url, ok := dictStringField(options, "url")
	if !ok {
		return nil, fmt.Errorf("%s options.url is required", call.Callee.String())
	}
	var bodyReader io.Reader
	if value, found := dictField(options, "body"); found {
		var err error
		bodyReader, err = e.httpBodyReader(value, call.Callee.String())
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if headersValue, ok := dictField(options, "headers"); ok {
		if err := applyRequestHeaders(call, req, headersValue); err != nil {
			return nil, err
		}
	}
	timeout := 0
	if value, ok := dictField(options, "timeoutMs"); ok {
		n, ok := native.AsInt64(value)
		if !ok {
			return nil, fmt.Errorf("%s options.timeoutMs must be int", call.Callee.String())
		}
		timeout = int(n)
	}
	client := http.DefaultClient
	if timeout > 0 {
		client = &http.Client{Timeout: time.Duration(timeout) * time.Millisecond}
	}
	return doHTTPRequest(client, req)
}

// DefaultHTTPUserAgent is the User-Agent header value Geblang sets on
// every outgoing HTTP request that doesn't already specify one. Avoid
// the Go runtime's default "Go-http-client/1.1" so servers can identify
// Geblang traffic and so security/firewall filters can allow-list it.
const DefaultHTTPUserAgent = "Geblang/1.0"

// applyDefaultUserAgent sets the User-Agent header on req to the
// Geblang default if and only if no User-Agent has been set yet. Caller
// supplied headers always win.
func applyDefaultUserAgent(req *http.Request) {
	if req == nil || req.Header == nil {
		return
	}
	if req.Header.Get("User-Agent") != "" {
		return
	}
	req.Header.Set("User-Agent", DefaultHTTPUserAgent)
}

func doHTTPRequest(client *http.Client, req *http.Request) (runtime.Value, error) {
	applyDefaultUserAgent(req)
	return doHTTPRequestWithRetries(client, req, httpRetryOptions{})
}

// httpRetryOptions describes the retry behaviour of a single request. A
// zero value disables retries entirely (the request runs once). Network
// errors are always retried up to attempts; only the listed status codes
// trigger a retry for HTTP responses.
type httpRetryOptions struct {
	attempts        int   // total attempts (1 means no retry)
	backoffMs       int64 // base backoff between retries
	backoffMaxMs    int64 // upper bound on a single sleep
	retryStatuses   map[int]struct{}
	hasCustomStatus bool
}

func defaultRetryStatuses() map[int]struct{} {
	return map[int]struct{}{502: {}, 503: {}, 504: {}, 429: {}}
}

// doHTTPRequestWithRetries wraps doHTTPRequest with attempt looping and
// exponential backoff plus full jitter. The request body is buffered up
// front so it can be re-sent on each attempt.
func doHTTPRequestWithRetries(client *http.Client, req *http.Request, opts httpRetryOptions) (runtime.Value, error) {
	attempts := opts.attempts
	if attempts < 1 {
		attempts = 1
	}
	statuses := opts.retryStatuses
	if statuses == nil {
		statuses = defaultRetryStatuses()
	}
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	resetBody := func() {
		if bodyBytes == nil {
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.ContentLength = int64(len(bodyBytes))
	}
	resetBody()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < attempts {
				httpRetrySleep(opts, attempt)
				resetBody()
				continue
			}
			return nil, err
		}
		responseBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < attempts {
				httpRetrySleep(opts, attempt)
				resetBody()
				continue
			}
			return nil, readErr
		}
		_, shouldRetry := statuses[resp.StatusCode]
		if shouldRetry && attempt < attempts {
			httpRetrySleep(opts, attempt)
			resetBody()
			continue
		}
		headers := map[string]runtime.DictEntry{}
		for name, values := range resp.Header {
			key := runtime.String{Value: name}
			headers[dictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.String{Value: strings.Join(values, ",")}}
		}
		entries := map[string]runtime.DictEntry{}
		putDict(entries, "status", runtime.NewInt64(int64(resp.StatusCode)))
		putDict(entries, "body", runtime.String{Value: string(responseBody)})
		putDict(entries, "headers", runtime.Dict{Entries: headers})
		return runtime.Dict{Entries: entries}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("http request: exhausted %d attempts", attempts)
}

// httpRetrySleep waits before retry N (1-indexed). Sleep is exponential
// in the base backoff with full jitter, capped at backoffMaxMs.
func httpRetrySleep(opts httpRetryOptions, attempt int) {
	base := opts.backoffMs
	if base <= 0 {
		base = 100
	}
	max := opts.backoffMaxMs
	if max <= 0 {
		max = 5000
	}
	// Exponential growth: base, 2*base, 4*base, ...
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay > max {
			delay = max
			break
		}
	}
	// Full jitter: uniform in [0, delay).
	jitter := mrand.Int63n(delay + 1)
	time.Sleep(time.Duration(jitter) * time.Millisecond)
}

// httpRetryOptionsFromDict reads retries / retryBackoffMs /
// retryBackoffMaxMs / retryStatuses from an options dict. Missing keys
// leave the zero-value (which disables retries).
func httpRetryOptionsFromDict(opts runtime.Dict) (httpRetryOptions, error) {
	out := httpRetryOptions{}
	if v, ok := dictField(opts, "retries"); ok {
		if n, ok := toInt64(v); ok {
			out.attempts = int(n)
		}
	}
	if v, ok := dictField(opts, "retryBackoffMs"); ok {
		if n, ok := toInt64(v); ok {
			out.backoffMs = n
		}
	}
	if v, ok := dictField(opts, "retryBackoffMaxMs"); ok {
		if n, ok := toInt64(v); ok {
			out.backoffMaxMs = n
		}
	}
	if v, ok := dictField(opts, "retryStatuses"); ok {
		list, ok := v.(*runtime.List)
		if !ok {
			return out, fmt.Errorf("retryStatuses must be list<int>")
		}
		statuses := map[int]struct{}{}
		for _, elem := range list.Elements {
			n, ok := toInt64(elem)
			if !ok {
				return out, fmt.Errorf("retryStatuses must be list<int>")
			}
			statuses[int(n)] = struct{}{}
		}
		out.retryStatuses = statuses
		out.hasCustomStatus = true
	}
	return out, nil
}

func (e *Evaluator) websocketConnect(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects url and optional headers", call.Callee.String())
	}
	url, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s URL must be string", call.Callee.String())
	}
	headers := http.Header{}
	if len(args) == 2 {
		headerDict, ok := args[1].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s headers must be dict<string, string>", call.Callee.String())
		}
		for _, entry := range headerDict.Entries {
			key, keyOK := entry.Key.(runtime.String)
			value, valueOK := entry.Value.(runtime.String)
			if !keyOK || !valueOK {
				return nil, fmt.Errorf("%s headers must be dict<string, string>", call.Callee.String())
			}
			headers.Set(key.Value, value.Value)
		}
	}
	conn, _, err := websocket.DefaultDialer.Dial(url.Value, headers)
	if err != nil {
		return nil, err
	}
	return e.registerWebSocket(conn), nil
}

func websocketUpgradeResponse(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects handler", call.Callee.String())
	}
	handler, ok := args[0].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("%s handler must be function", call.Callee.String())
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "websocket", handler)
	return runtime.Dict{Entries: entries}, nil
}

func httpStreamResponse(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects handler", call.Callee.String())
	}
	handler, ok := args[0].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("%s handler must be function", call.Callee.String())
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "stream", handler)
	return runtime.Dict{Entries: entries}, nil
}

type wsHandle struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (h *wsHandle) writeMessage(messageType int, data []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return h.conn.WriteMessage(messageType, data)
}

func (e *Evaluator) registerWebSocket(conn *websocket.Conn) runtime.Value {
	e.wsMu.Lock()
	defer e.wsMu.Unlock()
	e.nextWSID++
	e.websockets[e.nextWSID] = &wsHandle{conn: conn}
	return runtime.NewInt64(e.nextWSID)
}

func (e *Evaluator) registerHTTPStream(w http.ResponseWriter, flusher http.Flusher) runtime.Value {
	e.httpStreamMu.Lock()
	defer e.httpStreamMu.Unlock()
	e.nextHTTPStreamID++
	e.httpStreams[e.nextHTTPStreamID] = &httpStreamHandle{writer: w, flusher: flusher}
	return runtime.NewInt64(e.nextHTTPStreamID)
}

func (e *Evaluator) httpStreamHandle(value runtime.Value) (*httpStreamHandle, error) {
	id, err := httpStreamHandleID(value)
	if err != nil {
		return nil, err
	}
	e.httpStreamMu.Lock()
	handle, ok := e.httpStreams[id]
	e.httpStreamMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.httpStreamHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown http stream handle %d", id)
	}
	return handle, nil
}

func (e *Evaluator) closeHTTPStreamID(id int64) error {
	e.httpStreamMu.Lock()
	handle, ok := e.httpStreams[id]
	if ok {
		handle.closed = true
		delete(e.httpStreams, id)
	}
	e.httpStreamMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.closeHTTPStreamID(id)
	}
	if !ok {
		return fmt.Errorf("unknown http stream handle %d", id)
	}
	return nil
}

func httpStreamHandleID(value runtime.Value) (int64, error) {
	handle, ok := value.(runtime.Int)
	if !ok || !handle.Value.IsInt64() {
		return 0, fmt.Errorf("stream must be http stream handle")
	}
	return handle.Value.Int64(), nil
}

func (e *Evaluator) httpStreamWrite(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects stream and text", call.Callee.String())
	}
	handle, err := e.httpStreamHandle(args[0])
	if err != nil {
		return nil, err
	}
	if handle.closed {
		return nil, fmt.Errorf("http stream is closed")
	}
	text, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s text must be string", call.Callee.String())
	}
	_, err = io.WriteString(handle.writer, text.Value)
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) httpStreamFlush(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects stream", call.Callee.String())
	}
	handle, err := e.httpStreamHandle(args[0])
	if err != nil {
		return nil, err
	}
	if handle.flusher != nil && !handle.closed {
		handle.flusher.Flush()
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) httpStreamClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects stream", call.Callee.String())
	}
	id, err := httpStreamHandleID(args[0])
	if err != nil {
		return nil, err
	}
	_ = e.closeHTTPStreamID(id)
	return runtime.Null{}, nil
}

func (e *Evaluator) websocketSendText(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects connection and text", call.Callee.String())
	}
	h, err := e.websocketHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	text, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s text must be string", call.Callee.String())
	}
	if err := h.writeMessage(websocket.TextMessage, []byte(text.Value)); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) websocketReadText(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects connection", call.Callee.String())
	}
	h, err := e.websocketHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	messageType, data, err := h.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.TextMessage {
		return nil, fmt.Errorf("%s received non-text message", call.Callee.String())
	}
	return runtime.String{Value: string(data)}, nil
}

func (e *Evaluator) websocketSendBytes(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects connection and bytes", call.Callee.String())
	}
	h, err := e.websocketHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	data, ok := args[1].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s data must be bytes", call.Callee.String())
	}
	if err := h.writeMessage(websocket.BinaryMessage, data.Value); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) websocketReadBytes(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects connection", call.Callee.String())
	}
	h, err := e.websocketHandle(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	_, data, err := h.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return runtime.Bytes{Value: data}, nil
}

func (e *Evaluator) websocketClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects connection", call.Callee.String())
	}
	id, err := websocketHandleID(args[0])
	if err != nil {
		return nil, fmt.Errorf("%s %w", call.Callee.String(), err)
	}
	return runtime.Null{}, e.closeWebSocketID(id)
}

func (e *Evaluator) closeWebSocketID(id int64) error {
	e.wsMu.Lock()
	h, ok := e.websockets[id]
	if ok {
		delete(e.websockets, id)
	}
	e.wsMu.Unlock()
	if !ok {
		return fmt.Errorf("unknown websocket connection %d", id)
	}
	return h.conn.Close()
}

func (e *Evaluator) websocketHandle(value runtime.Value) (*wsHandle, error) {
	id, err := websocketHandleID(value)
	if err != nil {
		return nil, err
	}
	e.wsMu.Lock()
	h, ok := e.websockets[id]
	e.wsMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.websocketHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown websocket connection %d", id)
	}
	return h, nil
}

func websocketHandleID(value runtime.Value) (int64, error) {
	handle, ok := value.(runtime.Int)
	if !ok || !handle.Value.IsInt64() {
		return 0, fmt.Errorf("connection must be websocket handle")
	}
	return handle.Value.Int64(), nil
}

func handleID(value runtime.Value, label string) (int64, error) {
	handle, ok := value.(runtime.Int)
	if !ok || !handle.Value.IsInt64() {
		return 0, fmt.Errorf("%s handle must be int", label)
	}
	return handle.Value.Int64(), nil
}

func (e *Evaluator) amqpDial(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects amqp url", call.Callee.String())
	}
	url, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s url must be string", call.Callee.String())
	}
	conn, err := amqp091.Dial(url.Value)
	if err != nil {
		return nil, fmt.Errorf("amqp.dial: %w", err)
	}
	e.amqpMu.Lock()
	defer e.amqpMu.Unlock()
	e.nextAmqpConnID++
	id := e.nextAmqpConnID
	e.amqpConns[id] = conn
	return runtime.NewInt64(id), nil
}

func (e *Evaluator) amqpChannel(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects connection handle", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp connection")
	if err != nil {
		return nil, err
	}
	e.amqpMu.Lock()
	conn, ok := e.amqpConns[id]
	e.amqpMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("amqp.channel: unknown connection handle %d", id)
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("amqp.channel: %w", err)
	}
	e.amqpMu.Lock()
	defer e.amqpMu.Unlock()
	e.nextAmqpChanID++
	chID := e.nextAmqpChanID
	e.amqpChans[chID] = ch
	return runtime.NewInt64(chID), nil
}

func (e *Evaluator) amqpDeclareQueue(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects channel, name, and optional opts", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp channel")
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.declareQueue: name must be string")
	}
	durable, autoDelete, exclusive := true, false, false
	if len(args) == 3 {
		opts, ok := args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("amqp.declareQueue: opts must be dict")
		}
		durable = dictBoolDefault(opts, "durable", true)
		autoDelete = dictBoolDefault(opts, "autoDelete", false)
		exclusive = dictBoolDefault(opts, "exclusive", false)
	}
	e.amqpMu.Lock()
	ch, ok := e.amqpChans[id]
	e.amqpMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("amqp.declareQueue: unknown channel handle %d", id)
	}
	q, err := ch.QueueDeclare(name.Value, durable, autoDelete, exclusive, false, nil)
	if err != nil {
		return nil, fmt.Errorf("amqp.declareQueue: %w", err)
	}
	entries := map[string]runtime.DictEntry{}
	nameKey := runtime.String{Value: "name"}
	entries["s"+"name"] = runtime.DictEntry{Key: nameKey, Value: runtime.String{Value: q.Name}}
	messagesKey := runtime.String{Value: "messages"}
	entries["s"+"messages"] = runtime.DictEntry{Key: messagesKey, Value: runtime.NewInt64(int64(q.Messages))}
	consumersKey := runtime.String{Value: "consumers"}
	entries["s"+"consumers"] = runtime.DictEntry{Key: consumersKey, Value: runtime.NewInt64(int64(q.Consumers))}
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) amqpDeclareExchange(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 3 || len(args) > 4 {
		return nil, fmt.Errorf("%s expects channel, name, kind, and optional opts", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp channel")
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.declareExchange: name must be string")
	}
	kind, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.declareExchange: kind must be string (fanout/topic/direct/headers)")
	}
	durable, autoDelete := true, false
	if len(args) == 4 {
		opts, ok := args[3].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("amqp.declareExchange: opts must be dict")
		}
		durable = dictBoolDefault(opts, "durable", true)
		autoDelete = dictBoolDefault(opts, "autoDelete", false)
	}
	e.amqpMu.Lock()
	ch, ok := e.amqpChans[id]
	e.amqpMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("amqp.declareExchange: unknown channel handle %d", id)
	}
	if err := ch.ExchangeDeclare(name.Value, kind.Value, durable, autoDelete, false, false, nil); err != nil {
		return nil, fmt.Errorf("amqp.declareExchange: %w", err)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) amqpQueueBind(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("%s expects channel, queue, exchange, routingKey", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp channel")
	if err != nil {
		return nil, err
	}
	queue, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.queueBind: queue must be string")
	}
	exchange, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.queueBind: exchange must be string")
	}
	routingKey, ok := args[3].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.queueBind: routingKey must be string")
	}
	e.amqpMu.Lock()
	ch, ok := e.amqpChans[id]
	e.amqpMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("amqp.queueBind: unknown channel handle %d", id)
	}
	if err := ch.QueueBind(queue.Value, routingKey.Value, exchange.Value, false, nil); err != nil {
		return nil, fmt.Errorf("amqp.queueBind: %w", err)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) amqpPublish(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 4 || len(args) > 5 {
		return nil, fmt.Errorf("%s expects channel, exchange, routingKey, body, and optional opts", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp channel")
	if err != nil {
		return nil, err
	}
	exchange, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.publish: exchange must be string")
	}
	routingKey, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.publish: routingKey must be string")
	}
	var body []byte
	switch b := args[3].(type) {
	case runtime.String:
		body = []byte(b.Value)
	case runtime.Bytes:
		body = b.Value
	default:
		return nil, fmt.Errorf("amqp.publish: body must be string or bytes")
	}
	contentType := "application/octet-stream"
	persistent := true
	if len(args) == 5 {
		opts, ok := args[4].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("amqp.publish: opts must be dict")
		}
		if v := dictStringDefault(opts, "contentType", ""); v != "" {
			contentType = v
		}
		persistent = dictBoolDefault(opts, "persistent", true)
	}
	e.amqpMu.Lock()
	ch, ok := e.amqpChans[id]
	e.amqpMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("amqp.publish: unknown channel handle %d", id)
	}
	deliveryMode := uint8(amqp091.Persistent)
	if !persistent {
		deliveryMode = uint8(amqp091.Transient)
	}
	err = ch.PublishWithContext(context.Background(), exchange.Value, routingKey.Value, false, false, amqp091.Publishing{
		ContentType:  contentType,
		Body:         body,
		DeliveryMode: deliveryMode,
	})
	if err != nil {
		return nil, fmt.Errorf("amqp.publish: %w", err)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) amqpGet(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects channel, queue, and optional autoAck", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp channel")
	if err != nil {
		return nil, err
	}
	queue, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("amqp.get: queue must be string")
	}
	autoAck := false
	if len(args) == 3 {
		b, ok := args[2].(runtime.Bool)
		if !ok {
			return nil, fmt.Errorf("amqp.get: autoAck must be bool")
		}
		autoAck = b.Value
	}
	e.amqpMu.Lock()
	ch, ok := e.amqpChans[id]
	e.amqpMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("amqp.get: unknown channel handle %d", id)
	}
	msg, ok, err := ch.Get(queue.Value, autoAck)
	if err != nil {
		return nil, fmt.Errorf("amqp.get: %w", err)
	}
	if !ok {
		return runtime.Null{}, nil
	}
	entries := map[string]runtime.DictEntry{}
	bodyKey := runtime.String{Value: "body"}
	entries["s"+"body"] = runtime.DictEntry{Key: bodyKey, Value: runtime.Bytes{Value: msg.Body}}
	tagKey := runtime.String{Value: "deliveryTag"}
	entries["s"+"deliveryTag"] = runtime.DictEntry{Key: tagKey, Value: runtime.NewInt64(int64(msg.DeliveryTag))}
	ctKey := runtime.String{Value: "contentType"}
	entries["s"+"contentType"] = runtime.DictEntry{Key: ctKey, Value: runtime.String{Value: msg.ContentType}}
	rkKey := runtime.String{Value: "routingKey"}
	entries["s"+"routingKey"] = runtime.DictEntry{Key: rkKey, Value: runtime.String{Value: msg.RoutingKey}}
	exchKey := runtime.String{Value: "exchange"}
	entries["s"+"exchange"] = runtime.DictEntry{Key: exchKey, Value: runtime.String{Value: msg.Exchange}}
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) amqpAck(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects channel and deliveryTag", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp channel")
	if err != nil {
		return nil, err
	}
	tag, ok := args[1].(runtime.Int)
	if !ok || !tag.Value.IsInt64() {
		return nil, fmt.Errorf("amqp.ack: deliveryTag must be int")
	}
	e.amqpMu.Lock()
	ch, ok := e.amqpChans[id]
	e.amqpMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("amqp.ack: unknown channel handle %d", id)
	}
	if err := ch.Ack(uint64(tag.Value.Int64()), false); err != nil {
		return nil, fmt.Errorf("amqp.ack: %w", err)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) amqpClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects handle", call.Callee.String())
	}
	id, err := handleID(args[0], "amqp")
	if err != nil {
		return nil, err
	}
	e.amqpMu.Lock()
	defer e.amqpMu.Unlock()
	if ch, ok := e.amqpChans[id]; ok {
		delete(e.amqpChans, id)
		_ = ch.Close()
		return runtime.Null{}, nil
	}
	if conn, ok := e.amqpConns[id]; ok {
		delete(e.amqpConns, id)
		_ = conn.Close()
		return runtime.Null{}, nil
	}
	return runtime.Null{}, nil
}

func dictBoolDefault(d runtime.Dict, key string, def bool) bool {
	for _, entry := range d.Entries {
		k, ok := entry.Key.(runtime.String)
		if !ok || k.Value != key {
			continue
		}
		if b, ok := entry.Value.(runtime.Bool); ok {
			return b.Value
		}
		return def
	}
	return def
}

func dictStringDefault(d runtime.Dict, key, def string) string {
	for _, entry := range d.Entries {
		k, ok := entry.Key.(runtime.String)
		if !ok || k.Value != key {
			continue
		}
		if s, ok := entry.Value.(runtime.String); ok {
			return s.Value
		}
		return def
	}
	return def
}

type kafkaReaderHandle struct {
	reader  *kafkago.Reader
	pending kafkago.Message
	hasMsg  bool
}

func dictStringList(d runtime.Dict, key string) []string {
	for _, entry := range d.Entries {
		k, ok := entry.Key.(runtime.String)
		if !ok || k.Value != key {
			continue
		}
		list, ok := entry.Value.(*runtime.List)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(list.Elements))
		for _, el := range list.Elements {
			if s, ok := el.(runtime.String); ok {
				out = append(out, s.Value)
			}
		}
		return out
	}
	return nil
}

func (e *Evaluator) kafkaWriter(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects an options dict", call.Callee.String())
	}
	opts, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("kafka.writer: opts must be dict")
	}
	brokers := dictStringList(opts, "brokers")
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafka.writer: opts.brokers must be a non-empty list<string>")
	}
	topic := dictStringDefault(opts, "topic", "")
	if topic == "" {
		return nil, fmt.Errorf("kafka.writer: opts.topic is required")
	}
	w := &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Topic:                  topic,
		Balancer:               &kafkago.Hash{},
		AllowAutoTopicCreation: dictBoolDefault(opts, "autoCreateTopic", false),
	}
	e.kafkaMu.Lock()
	defer e.kafkaMu.Unlock()
	e.nextKafkaWriterID++
	id := e.nextKafkaWriterID
	e.kafkaWriters[id] = w
	return runtime.NewInt64(id), nil
}

func (e *Evaluator) kafkaWrite(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects writer handle, value, and optional key", call.Callee.String())
	}
	id, err := handleID(args[0], "kafka writer")
	if err != nil {
		return nil, err
	}
	var value []byte
	switch v := args[1].(type) {
	case runtime.String:
		value = []byte(v.Value)
	case runtime.Bytes:
		value = v.Value
	default:
		return nil, fmt.Errorf("kafka.write: value must be string or bytes")
	}
	var key []byte
	if len(args) == 3 {
		switch k := args[2].(type) {
		case runtime.String:
			key = []byte(k.Value)
		case runtime.Bytes:
			key = k.Value
		case runtime.Null:
		default:
			return nil, fmt.Errorf("kafka.write: key must be string, bytes, or null")
		}
	}
	e.kafkaMu.Lock()
	w, ok := e.kafkaWriters[id]
	e.kafkaMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("kafka.write: unknown writer handle %d", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.WriteMessages(ctx, kafkago.Message{Key: key, Value: value}); err != nil {
		return nil, fmt.Errorf("kafka.write: %w", err)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) kafkaReader(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects an options dict", call.Callee.String())
	}
	opts, ok := args[0].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("kafka.reader: opts must be dict")
	}
	brokers := dictStringList(opts, "brokers")
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafka.reader: opts.brokers must be a non-empty list<string>")
	}
	topic := dictStringDefault(opts, "topic", "")
	if topic == "" {
		return nil, fmt.Errorf("kafka.reader: opts.topic is required")
	}
	groupID := dictStringDefault(opts, "groupId", "")
	if groupID == "" {
		return nil, fmt.Errorf("kafka.reader: opts.groupId is required")
	}
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
	})
	e.kafkaMu.Lock()
	defer e.kafkaMu.Unlock()
	e.nextKafkaReaderID++
	id := e.nextKafkaReaderID
	e.kafkaReaders[id] = &kafkaReaderHandle{reader: r}
	return runtime.NewInt64(id), nil
}

func (e *Evaluator) kafkaRead(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects reader handle and optional timeoutMs", call.Callee.String())
	}
	id, err := handleID(args[0], "kafka reader")
	if err != nil {
		return nil, err
	}
	timeoutMs := int64(30000)
	if len(args) == 2 {
		n, ok := args[1].(runtime.Int)
		if !ok || !n.Value.IsInt64() {
			return nil, fmt.Errorf("kafka.read: timeoutMs must be int")
		}
		timeoutMs = n.Value.Int64()
	}
	e.kafkaMu.Lock()
	handle, ok := e.kafkaReaders[id]
	e.kafkaMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("kafka.read: unknown reader handle %d", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	msg, err := handle.reader.FetchMessage(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return runtime.Null{}, nil
		}
		return nil, fmt.Errorf("kafka.read: %w", err)
	}
	handle.pending = msg
	handle.hasMsg = true
	entries := map[string]runtime.DictEntry{}
	put := func(key string, value runtime.Value) {
		k := runtime.String{Value: key}
		entries["s"+key] = runtime.DictEntry{Key: k, Value: value}
	}
	put("value", runtime.Bytes{Value: msg.Value})
	put("key", runtime.Bytes{Value: msg.Key})
	put("topic", runtime.String{Value: msg.Topic})
	put("partition", runtime.NewInt64(int64(msg.Partition)))
	put("offset", runtime.NewInt64(msg.Offset))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) kafkaCommit(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects reader handle", call.Callee.String())
	}
	id, err := handleID(args[0], "kafka reader")
	if err != nil {
		return nil, err
	}
	e.kafkaMu.Lock()
	handle, ok := e.kafkaReaders[id]
	e.kafkaMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("kafka.commit: unknown reader handle %d", id)
	}
	if !handle.hasMsg {
		return runtime.Null{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := handle.reader.CommitMessages(ctx, handle.pending); err != nil {
		return nil, fmt.Errorf("kafka.commit: %w", err)
	}
	handle.hasMsg = false
	return runtime.Null{}, nil
}

func (e *Evaluator) kafkaClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a writer or reader handle", call.Callee.String())
	}
	id, err := handleID(args[0], "kafka")
	if err != nil {
		return nil, err
	}
	e.kafkaMu.Lock()
	defer e.kafkaMu.Unlock()
	if w, ok := e.kafkaWriters[id]; ok {
		delete(e.kafkaWriters, id)
		_ = w.Close()
		return runtime.Null{}, nil
	}
	if r, ok := e.kafkaReaders[id]; ok {
		delete(e.kafkaReaders, id)
		_ = r.reader.Close()
		return runtime.Null{}, nil
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) httpServe(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects address, handler, and optional opts", call.Callee.String())
	}
	addr, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s address must be string", call.Callee.String())
	}
	handler, ok := args[1].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("%s handler must be a function", call.Callee.String())
	}
	pool, err := serverPoolFromArgs(args, 2, call.Callee.String())
	if err != nil {
		return nil, err
	}
	var serverOpts runtime.Value = runtime.Null{}
	if len(args) >= 3 {
		serverOpts = args[2]
	}
	tlsCfg, _, err := buildHTTPServerTLSConfig(serverOpts, addr.Value, call.Callee.String())
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:    addr.Value,
		Handler: e.httpHandler(handler, pool),
	}
	if tlsCfg != nil {
		server.TLSConfig = tlsCfg
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			return nil, err
		}
		return runtime.Null{}, nil
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) httpListen(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("%s expects address, handler, and optional opts", call.Callee.String())
	}
	addr, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s address must be string", call.Callee.String())
	}
	handler, ok := args[1].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("%s handler must be a function", call.Callee.String())
	}
	pool, err := serverPoolFromArgs(args, 2, call.Callee.String())
	if err != nil {
		return nil, err
	}
	var serverOpts runtime.Value = runtime.Null{}
	if len(args) >= 3 {
		serverOpts = args[2]
	}
	tlsCfg, certPEM, err := buildHTTPServerTLSConfig(serverOpts, addr.Value, call.Callee.String())
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", addr.Value)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: e.httpHandler(handler, pool)}
	handle := &httpServerHandle{server: server, listener: listener, done: make(chan error, 1), pool: pool, certPEM: certPEM}
	e.httpServerMu.Lock()
	e.nextHTTPServerID++
	id := e.nextHTTPServerID
	e.httpServers[id] = handle
	e.httpServerMu.Unlock()
	go func() {
		var err error
		if tlsCfg != nil {
			server.TLSConfig = tlsCfg
			err = server.ServeTLS(listener, "", "")
		} else {
			err = server.Serve(listener)
		}
		if err == http.ErrServerClosed {
			err = nil
		}
		handle.done <- err
		close(handle.done)
	}()
	return runtime.NewInt64(id), nil
}

// serverPoolFromArgs pulls the maxConcurrent/queueSize/onOverload
// dict at args[optsIndex] (if present) and returns a configured
// pool. Nil pool means unbounded.
func serverPoolFromArgs(args []runtime.Value, optsIndex int, label string) (*concurrent.Pool, error) {
	if len(args) <= optsIndex {
		return nil, nil
	}
	opts, ok := args[optsIndex].(runtime.Dict)
	if !ok {
		return nil, fmt.Errorf("%s opts must be dict", label)
	}
	maxConcurrent := 0
	if v, ok := dictField(opts, "maxConcurrent"); ok {
		n, ok := toInt64(v)
		if !ok || n < 0 {
			return nil, fmt.Errorf("%s opts.maxConcurrent must be a non-negative int", label)
		}
		maxConcurrent = int(n)
	}
	queueSize := 0
	if v, ok := dictField(opts, "queueSize"); ok {
		n, ok := toInt64(v)
		if !ok || n < 0 {
			return nil, fmt.Errorf("%s opts.queueSize must be a non-negative int", label)
		}
		queueSize = int(n)
	}
	policy := concurrent.Reject
	if v, ok := dictField(opts, "onOverload"); ok {
		s, ok := v.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s opts.onOverload must be string", label)
		}
		switch s.Value {
		case "reject", "wait", "drop":
			policy = concurrent.ParsePolicy(s.Value)
		default:
			return nil, fmt.Errorf("%s opts.onOverload must be \"reject\", \"wait\", or \"drop\"", label)
		}
	}
	if maxConcurrent == 0 && queueSize == 0 {
		return nil, nil
	}
	return concurrent.NewPool(maxConcurrent, queueSize, policy), nil
}

func (e *Evaluator) httpServerStats(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a server handle", call.Callee.String())
	}
	handle, err := e.httpServerHandle(args[0])
	if err != nil {
		return nil, err
	}
	return poolStatsDict(handle.pool), nil
}

func (e *Evaluator) netServerStats(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a server handle", call.Callee.String())
	}
	id, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s server handle must be int", call.Callee.String())
	}
	e.netServerMu.Lock()
	server, ok := e.netServers[id]
	e.netServerMu.Unlock()
	if !ok {
		if e.parent != nil {
			return e.parent.netServerStats(call, args)
		}
		return nil, fmt.Errorf("unknown net server handle %d", id)
	}
	return poolStatsDict(server.pool), nil
}

func poolStatsDict(pool *concurrent.Pool) runtime.Value {
	stats := pool.Stats()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "active", runtime.NewInt64(stats.Active))
	putDict(entries, "queued", runtime.NewInt64(stats.Queued))
	putDict(entries, "rejected", runtime.NewInt64(stats.Rejected))
	putDict(entries, "maxConcurrent", runtime.NewInt64(stats.MaxConcurrent))
	return runtime.Dict{Entries: entries}
}

func (e *Evaluator) httpServerAddr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a server handle", call.Callee.String())
	}
	handle, err := e.httpServerHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: handle.listener.Addr().String()}, nil
}

func (e *Evaluator) httpServerCert(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a server handle", call.Callee.String())
	}
	handle, err := e.httpServerHandle(args[0])
	if err != nil {
		return nil, err
	}
	if len(handle.certPEM) == 0 {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: string(handle.certPEM)}, nil
}

func (e *Evaluator) httpClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a server handle", call.Callee.String())
	}
	id, err := rawInt64(args[0], "http server handle")
	if err != nil {
		return nil, err
	}
	handle, ok := e.takeHTTPServerHandle(id)
	if !ok {
		return runtime.Null{}, nil
	}
	return runtime.Null{}, closeHTTPServerHandle(handle)
}

func (e *Evaluator) httpShutdown(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects server handle and optional timeoutMs", call.Callee.String())
	}
	id, err := rawInt64(args[0], "http server handle")
	if err != nil {
		return nil, err
	}
	timeout := int64(5000)
	if len(args) == 2 {
		timeout, err = rawInt64(args[1], "timeoutMs")
		if err != nil {
			return nil, err
		}
		if timeout < 0 {
			return nil, fmt.Errorf("%s timeoutMs must be non-negative", call.Callee.String())
		}
	}
	handle, ok := e.takeHTTPServerHandle(id)
	if !ok {
		return runtime.Null{}, nil
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
	}
	if err := handle.server.Shutdown(ctx); err != nil {
		return nil, err
	}
	if err := waitHTTPServerDone(handle); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) httpServerHandle(value runtime.Value) (*httpServerHandle, error) {
	id, err := rawInt64(value, "http server handle")
	if err != nil {
		return nil, err
	}
	e.httpServerMu.Lock()
	handle, ok := e.httpServers[id]
	e.httpServerMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.httpServerHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown http server handle %d", id)
	}
	return handle, nil
}

func (e *Evaluator) takeHTTPServerHandle(id int64) (*httpServerHandle, bool) {
	e.httpServerMu.Lock()
	handle, ok := e.httpServers[id]
	if ok {
		delete(e.httpServers, id)
	}
	e.httpServerMu.Unlock()
	if ok {
		return handle, true
	}
	if e.parent != nil {
		return e.parent.takeHTTPServerHandle(id)
	}
	return nil, false
}

func closeHTTPServerHandle(handle *httpServerHandle) error {
	if handle == nil || handle.server == nil {
		return nil
	}
	if err := handle.server.Close(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return waitHTTPServerDone(handle)
}

func waitHTTPServerDone(handle *httpServerHandle) error {
	if handle == nil || handle.done == nil {
		return nil
	}
	err, ok := <-handle.done
	if !ok {
		return nil
	}
	return err
}

func (e *Evaluator) httpHandler(handler runtime.Function, pool *concurrent.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pool != nil && !pool.IsUnbounded() {
			if err := pool.Acquire(r.Context()); err != nil {
				switch pool.Policy() {
				case concurrent.Drop:
					if hijacker, ok := w.(http.Hijacker); ok {
						if conn, _, hErr := hijacker.Hijack(); hErr == nil {
							_ = conn.Close()
							return
						}
					}
					w.WriteHeader(http.StatusServiceUnavailable)
				default:
					http.Error(w, "server at capacity", http.StatusServiceUnavailable)
				}
				return
			}
			defer pool.Release()
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		response, err := e.callHTTPHandler(handler, r, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		e.writeHTTPResponse(w, r, response)
	})
}

func (e *Evaluator) callHTTPHandler(handler runtime.Function, request *http.Request, body []byte) (runtime.Value, error) {
	callbackEval, callbackHandler := e.callbackEvaluator(handler)
	if callbackEval != e {
		defer callbackEval.Cleanup()
	}
	requestArg, err := callbackEval.httpRequestArgument(callbackHandler, request, body)
	if err != nil {
		return nil, err
	}
	return callbackEval.applyFunction(callbackHandler, []runtime.Value{requestArg})
}

func (e *Evaluator) callbackEvaluator(handler runtime.Function) (*Evaluator, runtime.Function) {
	if handler.Native != nil {
		return e, handler
	}
	child := e.childForCallback()
	return child, runtime.CloneFunction(handler)
}

func httpRequestValue(request *http.Request, body []byte) runtime.Value {
	return runtime.Dict{Entries: httpRequestEntries(request, body)}
}

func httpRequestEntries(request *http.Request, body []byte) map[string]runtime.DictEntry {
	headers := map[string]runtime.DictEntry{}
	for name, values := range request.Header {
		key := runtime.String{Value: name}
		headers[dictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.String{Value: strings.Join(values, ",")}}
	}
	entries := map[string]runtime.DictEntry{}
	put := func(key string, value runtime.Value) {
		keyValue := runtime.String{Value: key}
		entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
	}
	put("method", runtime.String{Value: request.Method})
	put("path", runtime.String{Value: request.URL.Path})
	put("query", runtime.String{Value: request.URL.RawQuery})
	put("remoteAddr", runtime.String{Value: request.RemoteAddr})
	put("body", runtime.String{Value: string(body)})
	put("headers", runtime.Dict{Entries: headers})
	return entries
}

func (e *Evaluator) httpRequestArgument(handler runtime.Function, request *http.Request, body []byte) (runtime.Value, error) {
	if !handlerWantsRequestObject(handler) {
		return httpRequestValue(request, body), nil
	}
	class := e.httpRequestClass
	if class == nil && e.parent != nil {
		class = e.parent.httpRequestClass
	}
	if class == nil {
		return nil, fmt.Errorf("Request class is not available")
	}
	return &runtime.Instance{Class: class, Fields: fieldsFromEntries(httpRequestEntries(request, body))}, nil
}

func handlerWantsRequestObject(handler runtime.Function) bool {
	if len(handler.Parameters) == 0 || handler.Parameters[0].Type == nil {
		return false
	}
	typ := handler.Parameters[0].Type
	return typ.Operator == "" && !typ.ListAlias && typeNamesEqual(typ.Name, "Request")
}

func (e *Evaluator) writeHTTPResponse(w http.ResponseWriter, r *http.Request, response runtime.Value) {
	if handler, ok := streamResponseHandler(response); ok {
		e.writeHTTPStreamResponse(w, response, handler)
		return
	}
	if handler, ok := websocketResponseHandler(response); ok {
		e.writeWebSocketResponse(w, r, response, handler)
		return
	}
	e.writeHTTPResponseValue(w, response)
}

func streamResponseHandler(response runtime.Value) (runtime.Function, bool) {
	if instance, ok := response.(*runtime.Instance); ok && strings.EqualFold(instance.Class.Name, "Response") {
		response = responseInstanceDict(instance)
	}
	dict, ok := response.(runtime.Dict)
	if !ok {
		return runtime.Function{}, false
	}
	value, ok := dictField(dict, "stream")
	if !ok {
		return runtime.Function{}, false
	}
	handler, ok := value.(runtime.Function)
	return handler, ok
}

func websocketResponseHandler(response runtime.Value) (runtime.Function, bool) {
	if instance, ok := response.(*runtime.Instance); ok && strings.EqualFold(instance.Class.Name, "Response") {
		response = responseInstanceDict(instance)
	}
	dict, ok := response.(runtime.Dict)
	if !ok {
		return runtime.Function{}, false
	}
	value, ok := dictField(dict, "websocket")
	if !ok {
		return runtime.Function{}, false
	}
	handler, ok := value.(runtime.Function)
	return handler, ok
}

func (e *Evaluator) writeWebSocketResponse(w http.ResponseWriter, r *http.Request, response runtime.Value, handler runtime.Function) {
	if dict, ok := response.(runtime.Dict); ok {
		if value, ok := dictField(dict, "headers"); ok {
			writeHTTPHeaders(w.Header(), value)
		}
	}
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	callbackEval, callbackHandler := e.callbackEvaluator(handler)
	if callbackEval != e {
		defer callbackEval.Cleanup()
	}
	handleValue := callbackEval.registerWebSocket(conn)
	handleID, _ := websocketHandleID(handleValue)
	defer func() {
		if err := callbackEval.closeWebSocketID(handleID); err != nil && !strings.Contains(err.Error(), "unknown websocket connection") {
			_ = conn.Close()
		}
	}()
	_, err = callbackEval.applyFunction(callbackHandler, []runtime.Value{handleValue})
	if err != nil {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()))
	}
}

func (e *Evaluator) writeHTTPStreamResponse(w http.ResponseWriter, response runtime.Value, handler runtime.Function) {
	status := http.StatusOK
	if dict, ok := response.(runtime.Dict); ok {
		if value, ok := dictField(dict, "status"); ok {
			if n, ok := toInt64(value); ok {
				status = int(n)
			}
		}
		if value, ok := dictField(dict, "headers"); ok {
			writeHTTPHeaders(w.Header(), value)
		}
	}
	flusher, _ := w.(http.Flusher)
	w.WriteHeader(status)
	if flusher != nil {
		flusher.Flush()
	}
	callbackEval, callbackHandler := e.callbackEvaluator(handler)
	if callbackEval != e {
		defer callbackEval.Cleanup()
	}
	handleValue := callbackEval.registerHTTPStream(w, flusher)
	handleID, _ := httpStreamHandleID(handleValue)
	defer callbackEval.closeHTTPStreamID(handleID)
	_, _ = callbackEval.applyFunction(callbackHandler, []runtime.Value{handleValue})
}

func writeHTTPResponse(w http.ResponseWriter, response runtime.Value) {
	(&Evaluator{}).writeHTTPResponseValue(w, response)
}

func writeHTTPHeaders(target http.Header, value runtime.Value) {
	headers, ok := httpHeaderValue(value)
	if !ok {
		return
	}
	for key, values := range headers.Values {
		for i, value := range values {
			if i == 0 {
				target.Set(key, value)
			} else {
				target.Add(key, value)
			}
		}
	}
}

func (e *Evaluator) writeHTTPResponseValue(w http.ResponseWriter, response runtime.Value) {
	if instance, ok := response.(*runtime.Instance); ok && strings.EqualFold(instance.Class.Name, "Response") {
		response = responseInstanceDict(instance)
	}
	status := http.StatusOK
	var body runtime.Value = runtime.Bytes{}
	if text, ok := response.(runtime.String); ok {
		body = text
	} else if dict, ok := response.(runtime.Dict); ok {
		if value, ok := dict.Entries[dictKey(runtime.String{Value: "status"})]; ok {
			if n, ok := toInt64(value.Value); ok {
				status = int(n)
			}
		}
		if value, ok := dict.Entries[dictKey(runtime.String{Value: "headers"})]; ok {
			writeHTTPHeaders(w.Header(), value.Value)
		}
		if value, ok := dict.Entries[dictKey(runtime.String{Value: "body"})]; ok {
			body = value.Value
		}
	} else if _, ok := response.(runtime.Null); !ok {
		body = runtime.String{Value: response.Inspect()}
	}
	w.WriteHeader(status)
	_ = e.writeHTTPBody(w, body)
}

func (e *Evaluator) writeHTTPBody(w http.ResponseWriter, body runtime.Value) error {
	switch body := body.(type) {
	case runtime.String:
		_, err := io.WriteString(w, body.Value)
		return err
	case runtime.Bytes:
		_, err := w.Write(body.Value)
		return err
	case runtime.Int:
		file, err := e.fileHandle(body)
		if err != nil {
			_, _ = io.WriteString(w, body.Inspect())
			return err
		}
		_, err = io.Copy(w, file)
		return err
	case runtime.NativeObject:
		if body.Kind == "IOBuffer" {
			buffer, err := e.bufferHandle(body.ID)
			if err != nil {
				_, _ = io.WriteString(w, body.Inspect())
				return err
			}
			_, err = io.Copy(w, bytes.NewReader(buffer.Bytes()))
			return err
		}
		_, err := io.WriteString(w, body.Inspect())
		return err
	case runtime.Null:
		return nil
	default:
		_, err := io.WriteString(w, body.Inspect())
		return err
	}
}

func builtinClass(env *runtime.Environment, name string) (*runtime.Class, error) {
	if env == nil {
		return nil, fmt.Errorf("%s class is not available", name)
	}
	value, ok := env.Get(name)
	if !ok {
		return nil, fmt.Errorf("%s class is not available", name)
	}
	class, ok := value.(*runtime.Class)
	if !ok {
		return nil, fmt.Errorf("%s is not a class", name)
	}
	return class, nil
}

func fieldsFromEntries(entries map[string]runtime.DictEntry) map[string]runtime.Value {
	fields := map[string]runtime.Value{}
	for _, entry := range entries {
		if key, ok := entry.Key.(runtime.String); ok {
			fields[key.Value] = entry.Value
		}
	}
	return fields
}

func responseInstanceDict(instance *runtime.Instance) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	status, ok := instance.Fields["status"]
	if !ok {
		status = runtime.NewInt64(http.StatusOK)
	}
	body, ok := instance.Fields["body"]
	if !ok {
		body = runtime.String{}
	}
	headers, ok := instance.Fields["headers"]
	if !ok {
		headers = runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	}
	if headerValue, ok := httpHeaderValue(headers); ok {
		headers = httpHeadersToDict(headerValue)
	}
	putDict(entries, "status", status)
	putDict(entries, "body", body)
	putDict(entries, "headers", headers)
	return runtime.Dict{Entries: entries}
}

func newResponseInstance(class *runtime.Class, status runtime.Value, body runtime.Value, headers runtime.Value) *runtime.Instance {
	if status == nil {
		status = runtime.NewInt64(http.StatusOK)
	}
	if s, ok := status.(runtime.SmallInt); ok {
		status = runtime.NewInt64(s.Value)
	}
	if body == nil {
		body = runtime.String{}
	}
	if headers == nil {
		headers = runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	}
	return &runtime.Instance{Class: class, Fields: map[string]runtime.Value{
		"status":  status,
		"body":    body,
		"headers": headers,
	}}
}

func (e *Evaluator) httpResponseObject(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if e.httpResponseClass == nil {
		return nil, fmt.Errorf("Response class is not available")
	}
	return buildResponseInstance(e.httpResponseClass, call.Callee.String(), args)
}

func (e *Evaluator) httpJSONResponseObject(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects value and optional status", call.Callee.String())
	}
	if e.httpResponseClass == nil {
		return nil, fmt.Errorf("Response class is not available")
	}
	status := runtime.Value(runtime.NewInt64(http.StatusOK))
	if len(args) == 2 {
		if _, ok := toInt64(args[1]); !ok {
			return nil, fmt.Errorf("%s status must be int", call.Callee.String())
		}
		status = args[1]
	}
	body, err := jsonStringFromValue(args[0])
	if err != nil {
		return nil, err
	}
	headers := map[string]runtime.DictEntry{}
	putDict(headers, "Content-Type", runtime.String{Value: "application/json"})
	return newResponseInstance(e.httpResponseClass, status, runtime.String{Value: body}, runtime.Dict{Entries: headers}), nil
}

func (e *Evaluator) httpNewClient(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects optional options dict", call.Callee.String())
	}
	h := &httpClientHandle{
		client:  &http.Client{},
		headers: http.Header{},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transportChanged := false
	if len(args) == 1 {
		opts, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s options must be dict", call.Callee.String())
		}
		if v, ok := dictField(opts, "timeoutMs"); ok {
			if ms, ok := toInt64(v); ok {
				h.client.Timeout = time.Duration(ms) * time.Millisecond
			}
		}
		if base, ok := dictStringField(opts, "baseUrl"); ok {
			h.baseURL = base
		}
		if hdrsVal, ok := dictField(opts, "headers"); ok {
			if hdrs, ok := httpHeaderValue(hdrsVal); ok {
				for k, vals := range hdrs.Values {
					for _, v := range vals {
						h.headers.Add(k, v)
					}
				}
			}
		}
		if jarVal, ok := dictField(opts, "cookieJar"); ok {
			jar, err := e.cookieJarFromValue(jarVal, call.Callee.String())
			if err != nil {
				return nil, err
			}
			h.client.Jar = jar
		}
		if keepAlive, ok := dictBoolField(opts, "keepAlive"); ok {
			transport.DisableKeepAlives = !keepAlive
			transportChanged = true
		}
		if maxIdle, ok := dictField(opts, "maxIdleConns"); ok {
			if n, ok := toInt64(maxIdle); ok {
				transport.MaxIdleConns = int(n)
				transport.MaxIdleConnsPerHost = int(n)
				transportChanged = true
			}
		}
		if proxyURL, ok := dictStringField(opts, "proxy"); ok {
			parsed, err := neturl.Parse(proxyURL)
			if err != nil {
				return nil, fmt.Errorf("%s proxy: %v", call.Callee.String(), err)
			}
			transport.Proxy = http.ProxyURL(parsed)
			transportChanged = true
		} else if useEnv, ok := dictBoolField(opts, "proxyFromEnv"); ok && useEnv {
			transport.Proxy = http.ProxyFromEnvironment
			transportChanged = true
		}
		if tlsVal, ok := dictField(opts, "tls"); ok {
			cfg, err := buildHTTPClientTLSConfig(tlsVal, call.Callee.String())
			if err != nil {
				return nil, err
			}
			if cfg != nil {
				transport.TLSClientConfig = cfg
				transportChanged = true
			}
		}
	}
	if transportChanged {
		h.client.Transport = transport
	}
	e.httpClientMu.Lock()
	id := e.nextHTTPClientID
	e.nextHTTPClientID++
	e.httpClientHandles[id] = h
	e.httpClientMu.Unlock()
	if e.httpClientClass == nil {
		return nil, fmt.Errorf("Client class is not initialized")
	}
	return &runtime.Instance{Class: e.httpClientClass, Fields: map[string]runtime.Value{"handle": runtime.NewInt64(id)}}, nil
}

// cookieJarFromValue resolves a cookieJar argument: either a CookieJar
// instance (created via http.newCookieJar()) or the bare value `true` to
// auto-create a fresh jar inline.
func (e *Evaluator) cookieJarFromValue(v runtime.Value, label string) (http.CookieJar, error) {
	if b, ok := v.(runtime.Bool); ok {
		if !b.Value {
			return nil, nil
		}
		jar, _ := cookiejar.New(nil)
		return jar, nil
	}
	inst, ok := v.(*runtime.Instance)
	if !ok || inst.Class == nil || inst.Class.Name != "CookieJar" {
		return nil, fmt.Errorf("%s cookieJar must be an http.CookieJar instance or true", label)
	}
	id, ok := inst.Fields["handle"].(runtime.Int)
	if !ok {
		return nil, fmt.Errorf("%s cookieJar handle invalid", label)
	}
	e.httpCookieJarMu.Lock()
	jar, found := e.httpCookieJars[id.Value.Int64()]
	e.httpCookieJarMu.Unlock()
	if !found {
		return nil, fmt.Errorf("%s cookieJar handle not found", label)
	}
	return jar, nil
}

func (e *Evaluator) httpNewCookieJar(call *ast.CallExpression, _ []runtime.Value) (runtime.Value, error) {
	jar, _ := cookiejar.New(nil)
	e.httpCookieJarMu.Lock()
	id := e.nextCookieJarID
	e.nextCookieJarID++
	e.httpCookieJars[id] = jar
	e.httpCookieJarMu.Unlock()
	if e.httpCookieJarClass == nil {
		return nil, fmt.Errorf("CookieJar class is not initialized")
	}
	return &runtime.Instance{Class: e.httpCookieJarClass, Fields: map[string]runtime.Value{"handle": runtime.NewInt64(id)}}, nil
}

func (e *Evaluator) httpBuild(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects url argument", call.Callee.String())
	}
	urlStr, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s url must be string", call.Callee.String())
	}
	if e.httpBuilderClass == nil {
		return nil, fmt.Errorf("Builder class is not initialized")
	}
	return &runtime.Instance{
		Class: e.httpBuilderClass,
		Fields: map[string]runtime.Value{
			"_url":       urlStr,
			"_method":    runtime.String{Value: "GET"},
			"_body":      runtime.Null{},
			"_timeoutMs": runtime.Null{},
			"_headers":   runtime.Null{},
		},
	}, nil
}

func httpBuildReqFromSpec(spec runtime.Dict) (*http.Request, string, error) {
	method := "GET"
	if v, ok := dictStringField(spec, "method"); ok {
		method = strings.ToUpper(v)
	}
	urlStr, ok := dictStringField(spec, "url")
	if !ok {
		return nil, "", fmt.Errorf("request spec missing url")
	}
	body := ""
	if v, ok := dictStringField(spec, "body"); ok {
		body = v
	}
	req, err := http.NewRequest(method, urlStr, strings.NewReader(body))
	if err != nil {
		return nil, urlStr, err
	}
	if hdrs, ok := dictField(spec, "headers"); ok {
		if err := applyRequestHeaders(nil, req, hdrs); err != nil {
			return nil, urlStr, err
		}
	}
	return req, urlStr, nil
}

func (e *Evaluator) httpFetchAll(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a list of request specs", call.Callee.String())
	}
	list, ok := args[0].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s argument must be a list", call.Callee.String())
	}
	specs := list.Elements
	task := runtime.NewTask()
	go func() {
		results := make([]runtime.Value, len(specs))
		var wg sync.WaitGroup
		for i, sv := range specs {
			wg.Add(1)
			go func(idx int, specVal runtime.Value) {
				defer wg.Done()
				d, ok := specVal.(runtime.Dict)
				if !ok {
					entries := map[string]runtime.DictEntry{}
					putDict(entries, "error", runtime.String{Value: "request spec must be a dict"})
					results[idx] = runtime.Dict{Entries: entries}
					return
				}
				req, _, reqErr := httpBuildReqFromSpec(d)
				if reqErr != nil {
					entries := map[string]runtime.DictEntry{}
					putDict(entries, "error", runtime.String{Value: reqErr.Error()})
					results[idx] = runtime.Dict{Entries: entries}
					return
				}
				result, doErr := doHTTPRequest(http.DefaultClient, req)
				if doErr != nil {
					entries := map[string]runtime.DictEntry{}
					putDict(entries, "error", runtime.String{Value: doErr.Error()})
					results[idx] = runtime.Dict{Entries: entries}
					return
				}
				results[idx] = result
			}(i, sv)
		}
		wg.Wait()
		task.Complete(&runtime.List{Elements: results}, nil)
	}()
	return task, nil
}

func (e *Evaluator) httpFetchStream(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a list of request specs", call.Callee.String())
	}
	list, ok := args[0].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s argument must be a list", call.Callee.String())
	}
	if e.httpFetchStreamClass == nil {
		return nil, fmt.Errorf("FetchStream class is not initialized")
	}
	specs := list.Elements
	ch := make(chan runtime.Value, len(specs))
	sh := &httpFetchStreamHandle{ch: ch, total: len(specs)}
	e.httpFetchStreamMu.Lock()
	id := e.nextFetchStreamID
	e.nextFetchStreamID++
	e.httpFetchStreams[id] = sh
	e.httpFetchStreamMu.Unlock()
	for i, specVal := range specs {
		go func(idx int, sv runtime.Value) {
			d, ok := sv.(runtime.Dict)
			if !ok {
				entries := map[string]runtime.DictEntry{}
				putDict(entries, "error", runtime.String{Value: "request spec must be a dict"})
				putDict(entries, "index", runtime.NewInt64(int64(idx)))
				ch <- runtime.Dict{Entries: entries}
				return
			}
			req, resolvedURL, reqErr := httpBuildReqFromSpec(d)
			if reqErr != nil {
				entries := map[string]runtime.DictEntry{}
				putDict(entries, "error", runtime.String{Value: reqErr.Error()})
				putDict(entries, "index", runtime.NewInt64(int64(idx)))
				putDict(entries, "url", runtime.String{Value: resolvedURL})
				ch <- runtime.Dict{Entries: entries}
				return
			}
			result, doErr := doHTTPRequest(http.DefaultClient, req)
			if doErr != nil {
				entries := map[string]runtime.DictEntry{}
				putDict(entries, "error", runtime.String{Value: doErr.Error()})
				putDict(entries, "index", runtime.NewInt64(int64(idx)))
				putDict(entries, "url", runtime.String{Value: resolvedURL})
				ch <- runtime.Dict{Entries: entries}
				return
			}
			if dict, ok := result.(runtime.Dict); ok {
				putDict(dict.Entries, "index", runtime.NewInt64(int64(idx)))
				putDict(dict.Entries, "url", runtime.String{Value: resolvedURL})
				ch <- dict
			} else {
				ch <- result
			}
		}(i, specVal)
	}
	return &runtime.Instance{
		Class:  e.httpFetchStreamClass,
		Fields: map[string]runtime.Value{"handle": runtime.NewInt64(id)},
	}, nil
}

func jsonStringFromValue(value runtime.Value) (string, error) {
	encoded, err := valueToJSON(value)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(encoded); err != nil {
		return "", err
	}
	return strings.TrimSuffix(body.String(), "\n"), nil
}

func buildResponseInstance(class *runtime.Class, label string, args []runtime.Value) (runtime.Value, error) {
	if len(args) == 1 {
		if dict, ok := args[0].(runtime.Dict); ok {
			status, _ := dictField(dict, "status")
			body, _ := dictField(dict, "body")
			headers, _ := dictField(dict, "headers")
			return newResponseInstance(class, status, body, headers), nil
		}
	}
	if len(args) > 3 {
		return nil, fmt.Errorf("%s expects optional status, body, and headers", label)
	}
	var status runtime.Value = runtime.NewInt64(http.StatusOK)
	var body runtime.Value = runtime.String{}
	var headers runtime.Value = runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	if len(args) >= 1 {
		if _, ok := toInt64(args[0]); !ok {
			return nil, fmt.Errorf("%s status must be int", label)
		}
		status = args[0]
	}
	if len(args) >= 2 {
		switch args[1].(type) {
		case runtime.String, runtime.Bytes, runtime.Int, runtime.NativeObject:
			body = args[1]
		default:
			body = runtime.String{Value: args[1].Inspect()}
		}
	}
	if len(args) >= 3 {
		if _, ok := httpHeaderValue(args[2]); !ok {
			return nil, fmt.Errorf("%s headers must be dict<string, string>", label)
		}
		headers = args[2]
	}
	return newResponseInstance(class, status, body, headers), nil
}

func ioReadText(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: string(data)}, nil
}

func ioWriteText(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	content, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s content must be string", call.Callee.String())
	}
	if err := os.WriteFile(path.Value, []byte(content.Value), 0o666); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func ioAppendText(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	content, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s content must be string", call.Callee.String())
	}
	file, err := os.OpenFile(path.Value, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := io.WriteString(file, content.Value); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func toInt64(v runtime.Value) (int64, bool) {
	switch x := v.(type) {
	case runtime.SmallInt:
		return x.Value, true
	case runtime.Int:
		if x.Value.IsInt64() {
			return x.Value.Int64(), true
		}
	}
	return 0, false
}

func (e *Evaluator) ioReadBytes(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) == 1 {
		path, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s path must be string", call.Callee.String())
		}
		data, err := os.ReadFile(path.Value)
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: data}, nil
	}
	if len(args) == 2 {
		n, ok := toInt64(args[1])
		if !ok {
			return nil, fmt.Errorf("%s byte count must be int", call.Callee.String())
		}
		if n < 0 || n > 1<<30 {
			return nil, fmt.Errorf("%s byte count out of range", call.Callee.String())
		}
		reader, err := e.ioReader(args[0])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n)
		read, err := reader.Read(buf)
		if err != nil && err != io.EOF {
			return nil, err
		}
		return runtime.Bytes{Value: buf[:read]}, nil
	}
	return nil, fmt.Errorf("%s expects path or file and byte count", call.Callee.String())
}

func (e *Evaluator) ioWriteBytes(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	data, ok := args[1].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s content must be bytes", call.Callee.String())
	}
	if path, ok := args[0].(runtime.String); ok {
		if err := os.WriteFile(path.Value, data.Value, 0o666); err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	}
	writer, err := e.ioWriter(args[0])
	if err != nil {
		return nil, err
	}
	written, err := writer.Write(data.Value)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(written)), nil
}

func ioAppendBytes(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	data, ok := args[1].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s content must be bytes", call.Callee.String())
	}
	file, err := os.OpenFile(path.Value, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Write(data.Value); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func ioExists(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return runtime.Bool{Value: true}, nil
	}
	if os.IsNotExist(err) {
		return runtime.Bool{Value: false}, nil
	}
	return nil, err
}

func ioTempFile(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	dir, pattern, err := tempArgs(call, args, "geblang-*")
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return nil, err
	}
	return runtime.String{Value: path}, nil
}

func ioTempDir(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	dir, pattern, err := tempArgs(call, args, "geblang-*")
	if err != nil {
		return nil, err
	}
	path, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: path}, nil
}

func tempArgs(call *ast.CallExpression, args []runtime.Value, defaultPattern string) (string, string, error) {
	switch len(args) {
	case 0:
		return "", defaultPattern, nil
	case 1:
		pattern, ok := args[0].(runtime.String)
		if !ok {
			return "", "", fmt.Errorf("%s pattern must be string", call.Callee.String())
		}
		return "", pattern.Value, nil
	case 2:
		dir, ok := args[0].(runtime.String)
		if !ok {
			return "", "", fmt.Errorf("%s dir must be string", call.Callee.String())
		}
		pattern, ok := args[1].(runtime.String)
		if !ok {
			return "", "", fmt.Errorf("%s pattern must be string", call.Callee.String())
		}
		return dir.Value, pattern.Value, nil
	default:
		return "", "", fmt.Errorf("%s expects zero, one, or two arguments", call.Callee.String())
	}
}

func (e *Evaluator) ioOpen(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	mode, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s mode must be string", call.Callee.String())
	}
	flags, err := fileOpenFlags(mode.Value)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path.Value, flags, 0o666)
	if err != nil {
		return nil, err
	}
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	e.nextFileID++
	e.files[e.nextFileID] = file
	return runtime.NewInt64(e.nextFileID), nil
}

func (e *Evaluator) ioMemory(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects zero or one argument", call.Callee.String())
	}
	mem := &memoryStream{}
	if len(args) == 1 {
		switch value := args[0].(type) {
		case runtime.String:
			_, _ = mem.Write([]byte(value.Value))
		case runtime.Bytes:
			_, _ = mem.Write(value.Value)
		default:
			return nil, fmt.Errorf("%s initial value must be string or bytes", call.Callee.String())
		}
	}
	return e.registerIOStream(&ioStreamHandle{name: "memory", reader: mem, writer: mem, memory: mem}), nil
}

func (e *Evaluator) ioStdout(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	return e.registerIOStream(&ioStreamHandle{name: "stdout", writer: writerFunc(func(p []byte) (int, error) {
		return e.stdout.Write(p)
	})}), nil
}

func (e *Evaluator) ioStderr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	return e.registerIOStream(&ioStreamHandle{name: "stderr", writer: writerFunc(func(p []byte) (int, error) {
		return e.stderr.Write(p)
	})}), nil
}

func (e *Evaluator) ioStdin(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	return e.registerIOStream(&ioStreamHandle{name: "stdin", reader: readerFunc(func(p []byte) (int, error) {
		return e.stdin.Read(p)
	})}), nil
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func (e *Evaluator) registerIOStream(handle *ioStreamHandle) runtime.Value {
	e.streamMu.Lock()
	defer e.streamMu.Unlock()
	e.nextStreamID++
	e.streams[e.nextStreamID] = handle
	return runtime.NativeObject{Kind: "IOStream", ID: e.nextStreamID}
}

func (e *Evaluator) ioStreamHandle(value runtime.Value) (*ioStreamHandle, error) {
	stream, ok := value.(runtime.NativeObject)
	if !ok || (stream.Kind != "IOStream" && stream.Kind != "IOCapture") {
		return nil, fmt.Errorf("stream handle must be IOStream")
	}
	e.streamMu.Lock()
	handle, ok := e.streams[stream.ID]
	e.streamMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.ioStreamHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown stream handle %d", stream.ID)
	}
	if handle.closed {
		return nil, fmt.Errorf("stream handle %d is closed", stream.ID)
	}
	return handle, nil
}

func (e *Evaluator) ioReader(value runtime.Value) (io.Reader, error) {
	if stream, ok := isIOStreamKind(value); ok {
		handle, err := e.ioStreamHandle(stream)
		if err != nil {
			return nil, err
		}
		if handle.reader == nil {
			return nil, fmt.Errorf("%s stream is not readable", handle.name)
		}
		if handle.bufReader != nil {
			return handle.bufReader, nil
		}
		return handle.reader, nil
	}
	return e.fileHandle(value)
}

func (e *Evaluator) ioWriter(value runtime.Value) (io.Writer, error) {
	if stream, ok := isIOStreamKind(value); ok {
		handle, err := e.ioStreamHandle(stream)
		if err != nil {
			return nil, err
		}
		if handle.writer == nil {
			return nil, fmt.Errorf("%s stream is not writable", handle.name)
		}
		return handle.writer, nil
	}
	return e.fileHandle(value)
}

func (e *Evaluator) ioRead(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	n, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s byte count must be int", call.Callee.String())
	}
	if n < 0 || n > 1<<30 {
		return nil, fmt.Errorf("%s byte count out of range", call.Callee.String())
	}
	reader, err := e.ioReader(args[0])
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	read, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return runtime.String{Value: string(buf[:read])}, nil
}

func (e *Evaluator) ioReadAll(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	reader, err := e.ioReader(args[0])
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: string(data)}, nil
}

func (e *Evaluator) ioWrite(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	text, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s content must be string", call.Callee.String())
	}
	if buffer, ok := args[0].(runtime.NativeObject); ok && buffer.Kind == "IOBuffer" {
		return e.writeBuffer(buffer.ID, text.Value)
	}
	writer, err := e.ioWriter(args[0])
	if err != nil {
		return nil, err
	}
	written, err := io.WriteString(writer, text.Value)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(written)), nil
}

func (e *Evaluator) ioWriteln(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	text, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s content must be string", call.Callee.String())
	}
	line := text.Value + "\n"
	if buffer, ok := args[0].(runtime.NativeObject); ok && buffer.Kind == "IOBuffer" {
		return e.writeBuffer(buffer.ID, line)
	}
	writer, err := e.ioWriter(args[0])
	if err != nil {
		return nil, err
	}
	written, err := io.WriteString(writer, line)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(written)), nil
}

func (e *Evaluator) ioFlush(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	if buffer, ok := args[0].(runtime.NativeObject); ok && buffer.Kind == "IOBuffer" {
		if _, err := e.bufferHandle(buffer.ID); err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	}
	if stream, ok := isIOStreamKind(args[0]); ok {
		if _, err := e.ioStreamHandle(stream); err != nil {
			return nil, err
		}
		return runtime.Null{}, nil
	}
	file, err := e.fileHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, file.Sync()
}

func (e *Evaluator) ioSync(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	file, err := e.fileHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, file.Sync()
}

func (e *Evaluator) ioDataSync(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	file, err := e.fileHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, file.Sync()
}

func (e *Evaluator) ioLock(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	file, mode, err := e.fileLockArgs(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, syscall.Flock(int(file.Fd()), mode)
}

func (e *Evaluator) ioTryLock(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	file, mode, err := e.fileLockArgs(call, args)
	if err != nil {
		return nil, err
	}
	err = syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
	if err == nil {
		return runtime.Bool{Value: true}, nil
	}
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return runtime.Bool{Value: false}, nil
	}
	return nil, err
}

func (e *Evaluator) ioUnlock(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	file, err := e.fileHandle(args[0])
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func (e *Evaluator) fileLockArgs(call *ast.CallExpression, args []runtime.Value) (*os.File, int, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, 0, fmt.Errorf("%s expects file and optional mode", call.Callee.String())
	}
	file, err := e.fileHandle(args[0])
	if err != nil {
		return nil, 0, err
	}
	mode := "exclusive"
	if len(args) == 2 {
		value, ok := args[1].(runtime.String)
		if !ok {
			return nil, 0, fmt.Errorf("%s mode must be string", call.Callee.String())
		}
		mode = value.Value
	}
	switch mode {
	case "exclusive":
		return file, syscall.LOCK_EX, nil
	case "shared":
		return file, syscall.LOCK_SH, nil
	default:
		return nil, 0, fmt.Errorf("%s mode must be \"exclusive\" or \"shared\"", call.Callee.String())
	}
}

func (e *Evaluator) ioClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	if buffer, ok := args[0].(runtime.NativeObject); ok && buffer.Kind == "IOBuffer" {
		return e.closeBuffer(buffer.ID)
	}
	if stream, ok := isIOStreamKind(args[0]); ok {
		e.streamMu.Lock()
		handle, ok := e.streams[stream.ID]
		if ok {
			delete(e.streams, stream.ID)
		}
		e.streamMu.Unlock()
		if !ok && e.parent != nil {
			return e.parent.ioClose(call, args)
		}
		if !ok {
			return nil, fmt.Errorf("unknown stream handle %d", stream.ID)
		}
		return runtime.Null{}, closeIOStreamHandle(handle)
	}
	handle, err := fileHandleID(args[0])
	if err != nil {
		return nil, err
	}
	e.fileMu.Lock()
	file, ok := e.files[handle]
	if ok {
		delete(e.files, handle)
	}
	e.fileMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown file handle %d", handle)
	}
	if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) ioToString(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	if buffer, ok := args[0].(runtime.NativeObject); ok && buffer.Kind == "IOBuffer" {
		return e.bufferString(buffer.ID)
	}
	handle, err := e.ioStreamHandle(args[0])
	if err != nil {
		return nil, err
	}
	if handle.memory == nil {
		return nil, fmt.Errorf("%s stream has no in-memory content", handle.name)
	}
	return runtime.String{Value: handle.memory.String()}, nil
}

func (e *Evaluator) ioCaptureStdout(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	mem := &memoryStream{}
	previous := e.stdout
	handle := &ioStreamHandle{name: "stdout capture", reader: mem, writer: mem, memory: mem}
	handle.restore = func() {
		e.stdout = previous
	}
	e.stdout = mem
	value := e.registerIOStream(handle).(runtime.NativeObject)
	value.Kind = "IOCapture"
	return value, nil
}

func (e *Evaluator) ioCaptureStderr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	mem := &memoryStream{}
	previous := e.stderr
	handle := &ioStreamHandle{name: "stderr capture", reader: mem, writer: mem, memory: mem}
	handle.restore = func() {
		e.stderr = previous
	}
	e.stderr = mem
	value := e.registerIOStream(handle).(runtime.NativeObject)
	value.Kind = "IOCapture"
	return value, nil
}

func (e *Evaluator) ioRedirectStdout(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	writer, err := e.ioWriter(args[0])
	if err != nil {
		return nil, err
	}
	previous := e.stdout
	e.stdout = writer
	return runtime.Function{Name: "restoreStdout", Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("stdout restore expects no arguments")
		}
		e.stdout = previous
		return runtime.Null{}, nil
	}}, nil
}

func (e *Evaluator) ioRedirectStderr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	writer, err := e.ioWriter(args[0])
	if err != nil {
		return nil, err
	}
	previous := e.stderr
	e.stderr = writer
	return runtime.Function{Name: "restoreStderr", Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("stderr restore expects no arguments")
		}
		e.stderr = previous
		return runtime.Null{}, nil
	}}, nil
}

func (e *Evaluator) ioRedirectStdin(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	reader, err := e.ioReader(args[0])
	if err != nil {
		return nil, err
	}
	previous := e.stdin
	previousReader := e.stdinReader
	e.stdin = reader
	e.stdinReader = nil
	return runtime.Function{Name: "restoreStdin", Native: func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("stdin restore expects no arguments")
		}
		e.stdin = previous
		e.stdinReader = previousReader
		return runtime.Null{}, nil
	}}, nil
}

func (e *Evaluator) ioBuffer(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	e.bufferMu.Lock()
	defer e.bufferMu.Unlock()
	e.nextBufferID++
	id := e.nextBufferID
	e.buffers[id] = &bytes.Buffer{}
	return runtime.NativeObject{Kind: "IOBuffer", ID: id}, nil
}

func (e *Evaluator) ioBufferToString(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	buffer, err := ioBufferObject(args[0])
	if err != nil {
		return nil, err
	}
	return e.bufferString(buffer.ID)
}

func (e *Evaluator) ioBufferReset(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	buffer, err := ioBufferObject(args[0])
	if err != nil {
		return nil, err
	}
	return e.resetBuffer(buffer.ID)
}

func ioReadCSV(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, err
	}
	rows := make([]runtime.Value, 0, len(records))
	for _, record := range records {
		row := make([]runtime.Value, 0, len(record))
		for _, field := range record {
			row = append(row, runtime.String{Value: field})
		}
		rows = append(rows, &runtime.List{Elements: row})
	}
	return &runtime.List{Elements: rows}, nil
}

func ioWriteCSV(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	rows, ok := args[1].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s rows must be list", call.Callee.String())
	}
	file, err := os.Create(path.Value)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	for _, rowValue := range rows.Elements {
		rowList, ok := rowValue.(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("%s rows must be list<list<any>>", call.Callee.String())
		}
		record := make([]string, 0, len(rowList.Elements))
		for _, field := range rowList.Elements {
			record = append(record, field.Inspect())
		}
		if err := writer.Write(record); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) ioStdinReadAll(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	data, err := io.ReadAll(e.stdin)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: string(data)}, nil
}

func (e *Evaluator) ioStdinReadLine(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	if e.stdinReader == nil {
		e.stdinReader = bufio.NewReader(e.stdin)
	}
	line, err := e.stdinReader.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if err == io.EOF && line == "" {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: line}, nil
}

func (e *Evaluator) fileBufReader(handle int64) (*bufio.Reader, error) {
	e.fileMu.Lock()
	defer e.fileMu.Unlock()
	if r, ok := e.bufReaders[handle]; ok {
		return r, nil
	}
	file, ok := e.files[handle]
	if !ok {
		if e.parent != nil {
			return e.parent.fileBufReader(handle)
		}
		return nil, fmt.Errorf("unknown file handle %d", handle)
	}
	r := bufio.NewReader(file)
	e.bufReaders[handle] = r
	return r, nil
}

func (e *Evaluator) ioReadLine(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	if stream, ok := isIOStreamKind(args[0]); ok {
		handle, err := e.ioStreamHandle(stream)
		if err != nil {
			return nil, err
		}
		if handle.reader == nil {
			return nil, fmt.Errorf("%s stream is not readable", handle.name)
		}
		if handle.bufReader == nil {
			handle.bufReader = bufio.NewReader(handle.reader)
		}
		line, err := handle.bufReader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if err == io.EOF && line == "" {
			return runtime.Null{}, nil
		}
		return runtime.String{Value: line}, nil
	}
	handle, err := fileHandleID(args[0])
	if err != nil {
		return nil, err
	}
	r, err := e.fileBufReader(handle)
	if err != nil {
		return nil, err
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if err == io.EOF && line == "" {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: line}, nil
}

func (e *Evaluator) ioReadLines(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	var r *bufio.Reader
	if stream, ok := isIOStreamKind(args[0]); ok {
		handle, err := e.ioStreamHandle(stream)
		if err != nil {
			return nil, err
		}
		if handle.reader == nil {
			return nil, fmt.Errorf("%s stream is not readable", handle.name)
		}
		if handle.bufReader == nil {
			handle.bufReader = bufio.NewReader(handle.reader)
		}
		r = handle.bufReader
	} else {
		handle, err := fileHandleID(args[0])
		if err != nil {
			return nil, err
		}
		r, err = e.fileBufReader(handle)
		if err != nil {
			return nil, err
		}
	}
	var lines []runtime.Value
	for {
		line, readErr := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" || readErr == nil {
			lines = append(lines, runtime.String{Value: line})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return &runtime.List{Elements: lines}, nil
}

func ioStat(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "name", runtime.String{Value: info.Name()})
	putDict(entries, "size", runtime.NewInt64(info.Size()))
	putDict(entries, "mode", runtime.NewInt64(int64(info.Mode().Perm())))
	putDict(entries, "isDir", runtime.Bool{Value: info.IsDir()})
	putDict(entries, "modUnix", runtime.NewInt64(info.ModTime().Unix()))
	return runtime.Dict{Entries: entries}, nil
}

func ioChmod(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	modeVal, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s mode must be int", call.Callee.String())
	}
	return runtime.Null{}, os.Chmod(path.Value, os.FileMode(modeVal))
}

func ioChown(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects exactly three arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	uid, uidOK := toInt64(args[1])
	gid, gidOK := toInt64(args[2])
	if !uidOK || !gidOK {
		return nil, fmt.Errorf("%s uid and gid must be ints", call.Callee.String())
	}
	return runtime.Null{}, os.Chown(path.Value, int(uid), int(gid))
}

func ioMkdir(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	path, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s path must be string", call.Callee.String())
	}
	modeVal, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s mode must be int", call.Callee.String())
	}
	return runtime.Null{}, os.MkdirAll(path.Value, os.FileMode(modeVal))
}

func ioRemove(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, os.RemoveAll(path)
}

func ioRename(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	oldPath, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s old path must be string", call.Callee.String())
	}
	newPath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s new path must be string", call.Callee.String())
	}
	return runtime.Null{}, os.Rename(oldPath.Value, newPath.Value)
}

func ioSymlink(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	target, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s target must be string", call.Callee.String())
	}
	linkPath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s link path must be string", call.Callee.String())
	}
	return runtime.Null{}, os.Symlink(target.Value, linkPath.Value)
}

func ioReadLink(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	target, err := os.Readlink(path)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: target}, nil
}

func ioListDir(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	path, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	values := make([]runtime.Value, 0, len(entries))
	for _, entry := range entries {
		values = append(values, runtime.String{Value: entry.Name()})
	}
	return &runtime.List{Elements: values}, nil
}

func ioWalkDir(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	root, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	var values []runtime.Value
	err = filepath.Walk(root, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		entries := map[string]runtime.DictEntry{}
		putDict(entries, "path", runtime.String{Value: p})
		putDict(entries, "name", runtime.String{Value: info.Name()})
		putDict(entries, "isDir", runtime.Bool{Value: info.IsDir()})
		putDict(entries, "size", runtime.NewInt64(info.Size()))
		putDict(entries, "modUnix", runtime.NewInt64(info.ModTime().Unix()))
		values = append(values, runtime.Dict{Entries: entries})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &runtime.List{Elements: values}, nil
}

func (e *Evaluator) fileHandle(value runtime.Value) (*os.File, error) {
	handle, err := fileHandleID(value)
	if err != nil {
		return nil, err
	}
	e.fileMu.Lock()
	file, ok := e.files[handle]
	e.fileMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.fileHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown file handle %d", handle)
	}
	return file, nil
}

type streamSource struct {
	Reader io.Reader
	Text   string
}

func (e *Evaluator) streamSourceReader(call *ast.CallExpression, value runtime.Value) (streamSource, error) {
	switch value := value.(type) {
	case runtime.String:
		return streamSource{Reader: strings.NewReader(value.Value), Text: value.Value}, nil
	case runtime.Bytes:
		text := string(value.Value)
		return streamSource{Reader: bytes.NewReader(value.Value), Text: text}, nil
	case runtime.Int:
		file, err := e.fileHandle(value)
		if err != nil {
			return streamSource{}, err
		}
		return streamSource{Reader: file}, nil
	case runtime.NativeObject:
		switch value.Kind {
		case "IOBuffer":
			buffer, err := e.bufferHandle(value.ID)
			if err != nil {
				return streamSource{}, err
			}
			data := append([]byte(nil), buffer.Bytes()...)
			return streamSource{Reader: bytes.NewReader(data), Text: string(data)}, nil
		case "IOStream", "IOCapture":
			handle, err := e.ioStreamHandle(value)
			if err != nil {
				return streamSource{}, err
			}
			if handle.memory != nil {
				data := handle.memory.Bytes()
				return streamSource{Reader: bytes.NewReader(data), Text: string(data)}, nil
			}
			if handle.reader != nil {
				return streamSource{Reader: handle.reader}, nil
			}
			return streamSource{}, fmt.Errorf("%s source stream is not readable", call.Callee.String())
		default:
			return streamSource{}, fmt.Errorf("%s source native object must be IOBuffer or IOStream, got %s", call.Callee.String(), value.Kind)
		}
	default:
		return streamSource{}, fmt.Errorf("%s source must be string, bytes, file handle, IOBuffer, or IOStream", call.Callee.String())
	}
}

func ioBufferObject(value runtime.Value) (runtime.NativeObject, error) {
	buffer, ok := value.(runtime.NativeObject)
	if !ok || buffer.Kind != "IOBuffer" {
		return runtime.NativeObject{}, fmt.Errorf("buffer handle must be IOBuffer")
	}
	return buffer, nil
}

func (e *Evaluator) bufferHandle(id int64) (*bytes.Buffer, error) {
	e.bufferMu.Lock()
	buffer, ok := e.buffers[id]
	e.bufferMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.bufferHandle(id)
	}
	if !ok {
		return nil, fmt.Errorf("unknown buffer handle %d", id)
	}
	return buffer, nil
}

func (e *Evaluator) writeBuffer(id int64, text string) (runtime.Value, error) {
	e.bufferMu.Lock()
	buffer, ok := e.buffers[id]
	e.bufferMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.writeBuffer(id, text)
	}
	if !ok {
		return nil, fmt.Errorf("unknown buffer handle %d", id)
	}
	written, err := buffer.WriteString(text)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(written)), nil
}

func (e *Evaluator) bufferString(id int64) (runtime.Value, error) {
	buffer, err := e.bufferHandle(id)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: buffer.String()}, nil
}

func (e *Evaluator) resetBuffer(id int64) (runtime.Value, error) {
	e.bufferMu.Lock()
	buffer, ok := e.buffers[id]
	e.bufferMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.resetBuffer(id)
	}
	if !ok {
		return nil, fmt.Errorf("unknown buffer handle %d", id)
	}
	buffer.Reset()
	return runtime.Null{}, nil
}

func (e *Evaluator) closeBuffer(id int64) (runtime.Value, error) {
	e.bufferMu.Lock()
	_, ok := e.buffers[id]
	if ok {
		delete(e.buffers, id)
	}
	e.bufferMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.closeBuffer(id)
	}
	if !ok {
		return nil, fmt.Errorf("unknown buffer handle %d", id)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) ioBufferMethod(buffer runtime.NativeObject, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "write":
		if len(args) != 1 {
			return nil, fmt.Errorf("IOBuffer.write expects exactly one argument")
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("IOBuffer.write content must be string")
		}
		return e.writeBuffer(buffer.ID, text.Value)
	case "writeln":
		if len(args) != 1 {
			return nil, fmt.Errorf("IOBuffer.writeln expects exactly one argument")
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("IOBuffer.writeln content must be string")
		}
		return e.writeBuffer(buffer.ID, text.Value+"\n")
	case "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("IOBuffer.toString expects no arguments")
		}
		return e.bufferString(buffer.ID)
	case "reset":
		if len(args) != 0 {
			return nil, fmt.Errorf("IOBuffer.reset expects no arguments")
		}
		return e.resetBuffer(buffer.ID)
	case "length":
		if len(args) != 0 {
			return nil, fmt.Errorf("IOBuffer.length expects no arguments")
		}
		value, err := e.bufferHandle(buffer.ID)
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(int64(value.Len())), nil
	case "close":
		if len(args) != 0 {
			return nil, fmt.Errorf("IOBuffer.close expects no arguments")
		}
		return e.closeBuffer(buffer.ID)
	default:
		return nil, fmt.Errorf("IOBuffer has no method %s", name)
	}
}

func (e *Evaluator) ioStreamMethod(stream runtime.NativeObject, name string, args []runtime.Value) (runtime.Value, error) {
	call := &ast.CallExpression{Callee: &ast.SelectorExpression{Object: &ast.Identifier{Value: stream.Kind}, Name: &ast.Identifier{Value: name}}}
	switch name {
	case "write":
		if len(args) != 1 {
			return nil, fmt.Errorf("%s.write expects exactly one argument", stream.Kind)
		}
		return e.ioWrite(call, []runtime.Value{stream, args[0]})
	case "writeln":
		if len(args) != 1 {
			return nil, fmt.Errorf("%s.writeln expects exactly one argument", stream.Kind)
		}
		return e.ioWriteln(call, []runtime.Value{stream, args[0]})
	case "writeBytes":
		if len(args) != 1 {
			return nil, fmt.Errorf("%s.writeBytes expects exactly one argument", stream.Kind)
		}
		return e.ioWriteBytes(call, []runtime.Value{stream, args[0]})
	case "read":
		if stream.Kind == "IOCapture" && len(args) == 0 {
			return e.ioToString(call, []runtime.Value{stream})
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("%s.read expects exactly one argument", stream.Kind)
		}
		return e.ioRead(call, []runtime.Value{stream, args[0]})
	case "readBytes":
		if len(args) != 1 {
			return nil, fmt.Errorf("%s.readBytes expects exactly one argument", stream.Kind)
		}
		return e.ioReadBytes(call, []runtime.Value{stream, args[0]})
	case "readAll":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.%s expects no arguments", stream.Kind, name)
		}
		return e.ioReadAll(call, []runtime.Value{stream})
	case "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.%s expects no arguments", stream.Kind, name)
		}
		return e.ioToString(call, []runtime.Value{stream})
	case "bytes":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.bytes expects no arguments", stream.Kind)
		}
		handle, err := e.ioStreamHandle(stream)
		if err != nil {
			return nil, err
		}
		if handle.memory == nil {
			return nil, fmt.Errorf("%s stream has no in-memory content", handle.name)
		}
		return runtime.Bytes{Value: handle.memory.Bytes()}, nil
	case "reset":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.reset expects no arguments", stream.Kind)
		}
		handle, err := e.ioStreamHandle(stream)
		if err != nil {
			return nil, err
		}
		if handle.memory == nil {
			return nil, fmt.Errorf("%s stream has no in-memory content", handle.name)
		}
		handle.memory.Reset()
		return runtime.Null{}, nil
	case "close":
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.close expects no arguments", stream.Kind)
		}
		return e.ioClose(call, []runtime.Value{stream})
	default:
		return nil, fmt.Errorf("%s has no method %s", stream.Kind, name)
	}
}

func fileHandleID(value runtime.Value) (int64, error) {
	id, ok := value.(runtime.Int)
	if !ok || !id.Value.IsInt64() {
		return 0, fmt.Errorf("file handle must be int")
	}
	return id.Value.Int64(), nil
}

func fileOpenFlags(mode string) (int, error) {
	switch mode {
	case "r":
		return os.O_RDONLY, nil
	case "w":
		return os.O_CREATE | os.O_WRONLY | os.O_TRUNC, nil
	case "a":
		return os.O_CREATE | os.O_WRONLY | os.O_APPEND, nil
	case "rw":
		return os.O_CREATE | os.O_RDWR, nil
	case "rw_trunc":
		return os.O_CREATE | os.O_RDWR | os.O_TRUNC, nil
	default:
		return 0, fmt.Errorf("unsupported file mode %q", mode)
	}
}

func sysCwd(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: cwd}, nil
}

func sysGetenv(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	name, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	value, ok := os.LookupEnv(name)
	if !ok {
		return runtime.Null{}, nil
	}
	return runtime.String{Value: value}, nil
}

func (e *Evaluator) sysArgs(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s expects no arguments", call.Callee.String())
	}
	values := make([]runtime.Value, 0, len(e.args))
	for _, arg := range e.args {
		values = append(values, runtime.String{Value: arg})
	}
	return &runtime.List{Elements: values}, nil
}

func sysRun(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s expects at least one argument", call.Callee.String())
	}
	spec, err := processSpecFromArgs(call, args)
	if err != nil {
		return nil, err
	}
	return runProcessSpec(spec)
}

func sysRunWithOptions(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	spec, err := processSpecFromOptions(call, args)
	if err != nil {
		return nil, err
	}
	return runProcessSpec(spec)
}

func runProcessSpec(spec processSpec) (runtime.Value, error) {
	cmd, cancel, ctx := commandFromSpec(spec)
	if cancel != nil {
		defer cancel()
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := int64(0)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = int64(exitErr.ExitCode())
		} else {
			return nil, err
		}
	}
	entries := map[string]runtime.DictEntry{}
	put := func(key string, value runtime.Value) {
		keyValue := runtime.String{Value: key}
		entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
	}
	put("code", runtime.NewInt64(exitCode))
	put("stdout", runtime.String{Value: stdout.String()})
	put("stderr", runtime.String{Value: stderr.String()})
	put("timedOut", runtime.Bool{Value: ctx.Err() == context.DeadlineExceeded})
	return runtime.Dict{Entries: entries}, nil
}

func sysShell(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	command, err := singleStringValue(call, args)
	if err != nil {
		return nil, err
	}
	shellCall := &ast.CallExpression{Callee: call.Callee}
	return sysRun(shellCall, []runtime.Value{runtime.String{Value: "sh"}, runtime.String{Value: "-c"}, runtime.String{Value: command}})
}

func processSpecFromCombinedArgs(call *ast.CallExpression, args []runtime.Value) (processSpec, error) {
	if len(args) < 1 {
		return processSpec{}, fmt.Errorf("%s expects at least one argument", call.Callee.String())
	}
	command, ok := args[0].(runtime.String)
	if !ok {
		return processSpec{}, fmt.Errorf("%s command must be string", call.Callee.String())
	}
	// Two forms: ("cmd", ["arg1", "arg2"]) or ("cmd", "arg1", "arg2", ...)
	if len(args) == 2 {
		if list, ok := args[1].(*runtime.List); ok {
			commandArgs := make([]string, 0, len(list.Elements))
			for _, elem := range list.Elements {
				s, ok := elem.(runtime.String)
				if !ok {
					return processSpec{}, fmt.Errorf("%s argument list elements must be strings", call.Callee.String())
				}
				commandArgs = append(commandArgs, s.Value)
			}
			return processSpec{command: command.Value, args: commandArgs}, nil
		}
	}
	commandArgs := make([]string, 0, len(args)-1)
	for _, arg := range args[1:] {
		s, ok := arg.(runtime.String)
		if !ok {
			return processSpec{}, fmt.Errorf("%s arguments must be strings", call.Callee.String())
		}
		commandArgs = append(commandArgs, s.Value)
	}
	return processSpec{command: command.Value, args: commandArgs}, nil
}

func processSpecFromArgs(call *ast.CallExpression, args []runtime.Value) (processSpec, error) {
	command, ok := args[0].(runtime.String)
	if !ok {
		return processSpec{}, fmt.Errorf("%s command must be string", call.Callee.String())
	}
	commandArgs := make([]string, 0, len(args)-1)
	for _, arg := range args[1:] {
		value, ok := arg.(runtime.String)
		if !ok {
			return processSpec{}, fmt.Errorf("%s arguments must be strings", call.Callee.String())
		}
		commandArgs = append(commandArgs, value.Value)
	}
	return processSpec{command: command.Value, args: commandArgs}, nil
}

func processSpecFromOptions(call *ast.CallExpression, args []runtime.Value) (processSpec, error) {
	if len(args) != 1 {
		return processSpec{}, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	options, ok := args[0].(runtime.Dict)
	if !ok {
		return processSpec{}, fmt.Errorf("%s options must be dict", call.Callee.String())
	}
	command, ok := dictStringField(options, "command")
	if !ok || command == "" {
		return processSpec{}, fmt.Errorf("%s options.command must be string", call.Callee.String())
	}
	spec := processSpec{command: command}
	if value, ok := dictField(options, "args"); ok {
		list, ok := value.(*runtime.List)
		if !ok {
			return processSpec{}, fmt.Errorf("%s options.args must be list<string>", call.Callee.String())
		}
		spec.args = make([]string, 0, len(list.Elements))
		for _, element := range list.Elements {
			text, ok := element.(runtime.String)
			if !ok {
				return processSpec{}, fmt.Errorf("%s options.args must be list<string>", call.Callee.String())
			}
			spec.args = append(spec.args, text.Value)
		}
	}
	if cwd, ok := dictStringField(options, "cwd"); ok {
		spec.cwd = cwd
	}
	if value, ok := dictField(options, "env"); ok {
		env, ok := value.(runtime.Dict)
		if !ok {
			return processSpec{}, fmt.Errorf("%s options.env must be dict<string, string>", call.Callee.String())
		}
		spec.env = map[string]string{}
		for _, entry := range env.Entries {
			key, keyOK := entry.Key.(runtime.String)
			value, valueOK := entry.Value.(runtime.String)
			if !keyOK || !valueOK {
				return processSpec{}, fmt.Errorf("%s options.env must be dict<string, string>", call.Callee.String())
			}
			spec.env[key.Value] = value.Value
		}
	}
	if value, ok := dictField(options, "timeoutMs"); ok {
		n, ok := native.AsInt64(value)
		if !ok {
			return processSpec{}, fmt.Errorf("%s options.timeoutMs must be int", call.Callee.String())
		}
		if n < 0 {
			return processSpec{}, fmt.Errorf("%s options.timeoutMs must be >= 0", call.Callee.String())
		}
		spec.timeout = time.Duration(n) * time.Millisecond
	}
	return spec, nil
}

func commandFromSpec(spec processSpec) (*exec.Cmd, context.CancelFunc, context.Context) {
	ctx := context.Background()
	var cancel context.CancelFunc
	if spec.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, spec.timeout)
	}
	cmd := exec.CommandContext(ctx, spec.command, spec.args...)
	if spec.cwd != "" {
		cmd.Dir = spec.cwd
	}
	if len(spec.env) > 0 {
		env := os.Environ()
		for key, value := range spec.env {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}
	return cmd, cancel, ctx
}

func (e *Evaluator) sysStart(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s expects at least one argument", call.Callee.String())
	}
	spec, err := processSpecFromArgs(call, args)
	if err != nil {
		return nil, err
	}
	return e.startProcessSpec(spec)
}

func (e *Evaluator) sysStartWithOptions(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	spec, err := processSpecFromOptions(call, args)
	if err != nil {
		return nil, err
	}
	return e.startProcessSpec(spec)
}

func (e *Evaluator) startProcessSpec(spec processSpec) (runtime.Value, error) {
	cmd, cancel, _ := commandFromSpec(spec)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	e.processMu.Lock()
	defer e.processMu.Unlock()
	e.nextProcID++
	e.processes[e.nextProcID] = &processHandle{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, cancel: cancel}
	return runtime.NewInt64(e.nextProcID), nil
}

func (e *Evaluator) sysProcessWrite(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	process, err := e.processHandle(args[0])
	if err != nil {
		return nil, err
	}
	text, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s content must be string", call.Callee.String())
	}
	n, err := io.WriteString(process.stdin, text.Value)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(int64(n)), nil
}

func (e *Evaluator) sysProcessCloseStdin(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, process.stdin.Close()
}

func (e *Evaluator) sysProcessReadStdout(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(process.stdout)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: string(data)}, nil
}

func (e *Evaluator) sysProcessReadStderr(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(process.stderr)
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: string(data)}, nil
}

func (e *Evaluator) sysProcessReadStdoutN(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, size, err := e.processReadNArgs(call, args)
	if err != nil {
		return nil, err
	}
	return readProcessPipeN(call, process.stdout, size)
}

func (e *Evaluator) sysProcessReadStderrN(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, size, err := e.processReadNArgs(call, args)
	if err != nil {
		return nil, err
	}
	return readProcessPipeN(call, process.stderr, size)
}

func (e *Evaluator) processReadNArgs(call *ast.CallExpression, args []runtime.Value) (*processHandle, int64, error) {
	if len(args) != 2 {
		return nil, 0, fmt.Errorf("%s expects process and byte count", call.Callee.String())
	}
	process, err := e.processHandle(args[0])
	if err != nil {
		return nil, 0, err
	}
	n, ok := toInt64(args[1])
	if !ok {
		return nil, 0, fmt.Errorf("%s byte count must be int", call.Callee.String())
	}
	if n < 0 || n > 1<<30 {
		return nil, 0, fmt.Errorf("%s byte count out of range", call.Callee.String())
	}
	return process, n, nil
}

func readProcessPipeN(call *ast.CallExpression, reader io.Reader, n int64) (runtime.Value, error) {
	buf := make([]byte, n)
	read, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return runtime.String{Value: string(buf[:read])}, nil
}

func (e *Evaluator) sysProcessWait(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	handle, err := singleProcessHandleID(call, args)
	if err != nil {
		return nil, err
	}
	e.processMu.Lock()
	process, ok := e.processes[handle]
	if ok {
		delete(e.processes, handle)
	}
	e.processMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown process handle %d", handle)
	}
	err = process.cmd.Wait()
	if process.cancel != nil {
		process.cancel()
	}
	code := int64(0)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = int64(exitErr.ExitCode())
		} else {
			return nil, err
		}
	}
	return runtime.NewInt64(code), nil
}

func (e *Evaluator) sysProcessKill(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	if process.cancel != nil {
		process.cancel()
	}
	return runtime.Null{}, process.cmd.Process.Kill()
}

func (e *Evaluator) sysProcessSignal(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects process and signal name", call.Callee.String())
	}
	process, err := e.processHandle(args[0])
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s signal name must be string", call.Callee.String())
	}
	signal, err := signalByName(name.Value)
	if err != nil {
		return nil, err
	}
	if process.cancel != nil && signal == syscall.SIGKILL {
		process.cancel()
	}
	return runtime.Null{}, process.cmd.Process.Signal(signal)
}

func (e *Evaluator) sysProcessPid(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	if process.cmd.Process == nil {
		return nil, fmt.Errorf("process has not started")
	}
	return runtime.NewInt64(int64(process.cmd.Process.Pid)), nil
}

func signalByName(name string) (os.Signal, error) {
	switch strings.ToUpper(strings.TrimPrefix(name, "SIG")) {
	case "TERM":
		return syscall.SIGTERM, nil
	case "KILL":
		return syscall.SIGKILL, nil
	case "INT":
		return syscall.SIGINT, nil
	case "HUP":
		return syscall.SIGHUP, nil
	case "QUIT":
		return syscall.SIGQUIT, nil
	default:
		return nil, fmt.Errorf("unsupported signal %q", name)
	}
}

func (e *Evaluator) singleProcessHandle(call *ast.CallExpression, args []runtime.Value) (*processHandle, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	return e.processHandle(args[0])
}

func (e *Evaluator) processHandle(value runtime.Value) (*processHandle, error) {
	handle, err := processHandleID(value)
	if err != nil {
		return nil, err
	}
	e.processMu.Lock()
	process, ok := e.processes[handle]
	e.processMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.processHandle(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown process handle %d", handle)
	}
	return process, nil
}

func singleProcessHandleID(call *ast.CallExpression, args []runtime.Value) (int64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	return processHandleID(args[0])
}

func processHandleID(value runtime.Value) (int64, error) {
	id, ok := native.AsInt64(value)
	if !ok {
		return 0, fmt.Errorf("process handle must be int")
	}
	return id, nil
}

// ptyEIOReader wraps the master side of a pseudo-terminal so that
// the Linux-specific EIO returned after the child closes its end
// reads as io.EOF. POSIX defines this behaviour for ptys; Geblang
// users shouldn't see the raw errno.
type ptyEIOReader struct {
	r io.Reader
}

func (p *ptyEIOReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if err != nil {
		if pathErr, ok := err.(*os.PathError); ok && pathErr.Err == syscall.EIO {
			return n, io.EOF
		}
	}
	return n, err
}

// procSpawn starts a child process and returns a dict with handle,
// pid, and three IOStream handles (stdin, stdout, stderr). PTY mode
// (opts["pty"] == true) uses github.com/creack/pty so stdin/stdout
// share the master pty fd and stderr is null.
func (e *Evaluator) procSpawn(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects command, optional args list, optional options", call.Callee.String())
	}
	cmdName, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s command must be string", call.Callee.String())
	}
	var cmdArgs []string
	if len(args) >= 2 {
		switch a := args[1].(type) {
		case *runtime.List:
			cmdArgs = make([]string, 0, len(a.Elements))
			for _, elem := range a.Elements {
				s, ok := elem.(runtime.String)
				if !ok {
					return nil, fmt.Errorf("%s args list elements must be strings", call.Callee.String())
				}
				cmdArgs = append(cmdArgs, s.Value)
			}
		case runtime.Null:
			cmdArgs = nil
		default:
			return nil, fmt.Errorf("%s args must be a list of strings or null", call.Callee.String())
		}
	}
	usePTY := false
	var workDir string
	var envEntries []string
	if len(args) == 3 {
		opts, ok := args[2].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s options must be a dict", call.Callee.String())
		}
		if value, found := dictField(opts, "pty"); found {
			b, ok := value.(runtime.Bool)
			if !ok {
				return nil, fmt.Errorf("%s options.pty must be bool", call.Callee.String())
			}
			usePTY = b.Value
		}
		if value, found := dictField(opts, "cwd"); found {
			s, ok := value.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("%s options.cwd must be string", call.Callee.String())
			}
			workDir = s.Value
		}
		if value, found := dictField(opts, "env"); found {
			if d, ok := value.(runtime.Dict); ok {
				for _, entry := range d.Entries {
					k, kok := entry.Key.(runtime.String)
					v, vok := entry.Value.(runtime.String)
					if !kok || !vok {
						return nil, fmt.Errorf("%s options.env keys and values must be strings", call.Callee.String())
					}
					envEntries = append(envEntries, k.Value+"="+v.Value)
				}
			} else {
				return nil, fmt.Errorf("%s options.env must be a dict", call.Callee.String())
			}
		}
	}
	cmd := exec.Command(cmdName.Value, cmdArgs...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if envEntries != nil {
		cmd.Env = envEntries
	}
	handle := &processHandle{cmd: cmd}
	var stdinStream, stdoutStream, stderrStream *ioStreamHandle
	if usePTY {
		ptyFile, err := pty.Start(cmd)
		if err != nil {
			return nil, err
		}
		handle.stdin = ptyFile
		handle.stdout = ptyFile
		var ptyCloseOnce sync.Once
		handle.cancel = func() {
			ptyCloseOnce.Do(func() { _ = ptyFile.Close() })
		}
		// On Linux, reading the pty master after the child exits
		// returns EIO; translate to EOF so io.read* gives a clean
		// end-of-stream signal rather than surfacing the errno.
		ptyReader := &ptyEIOReader{r: ptyFile}
		// Both stdin and stdout streams alias the master pty fd. The
		// fd itself is closed by handle.cancel (which procWait /
		// procKill invoke); the per-stream Close() should be a no-op
		// so stopping a closed handle stays idempotent.
		stdinStream = &ioStreamHandle{name: "proc stdin (pty)", writer: ptyFile}
		stdoutStream = &ioStreamHandle{name: "proc stdout (pty)", reader: ptyReader}
		stderrStream = nil
	} else {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			return nil, err
		}
		handle.stdin = stdin
		handle.stdout = stdout
		handle.stderr = stderr
		stdinStream = &ioStreamHandle{name: "proc stdin", writer: stdin, closer: stdin}
		stdoutStream = &ioStreamHandle{name: "proc stdout", reader: stdout}
		stderrStream = &ioStreamHandle{name: "proc stderr", reader: stderr}
	}
	e.processMu.Lock()
	e.nextProcID++
	procID := e.nextProcID
	e.processes[procID] = handle
	e.processMu.Unlock()
	stdinVal := e.registerIOStream(stdinStream)
	stdoutVal := e.registerIOStream(stdoutStream)
	var stderrVal runtime.Value = runtime.Null{}
	if stderrStream != nil {
		stderrVal = e.registerIOStream(stderrStream)
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "handle", runtime.NewInt64(procID))
	putDict(entries, "pid", runtime.NewInt64(int64(cmd.Process.Pid)))
	putDict(entries, "stdin", stdinVal)
	putDict(entries, "stdout", stdoutVal)
	putDict(entries, "stderr", stderrVal)
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) procWait(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	err = process.cmd.Wait()
	if process.cancel != nil {
		process.cancel()
	}
	code := int64(0)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = int64(exitErr.ExitCode())
		} else {
			return nil, err
		}
	}
	return runtime.NewInt64(code), nil
}

func (e *Evaluator) procKill(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	if process.cancel != nil {
		process.cancel()
	}
	if process.cmd.Process == nil {
		return runtime.Null{}, nil
	}
	return runtime.Null{}, process.cmd.Process.Kill()
}

func (e *Evaluator) procSignal(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (process, signalName)", call.Callee.String())
	}
	process, err := e.processHandle(args[0])
	if err != nil {
		return nil, err
	}
	name, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s signal name must be string", call.Callee.String())
	}
	signal, err := signalByName(name.Value)
	if err != nil {
		return nil, err
	}
	if process.cmd.Process == nil {
		return nil, fmt.Errorf("process has not started")
	}
	if process.cancel != nil && signal == syscall.SIGKILL {
		process.cancel()
	}
	return runtime.Null{}, process.cmd.Process.Signal(signal)
}

func (e *Evaluator) procPid(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	process, err := e.singleProcessHandle(call, args)
	if err != nil {
		return nil, err
	}
	if process.cmd.Process == nil {
		return nil, fmt.Errorf("process has not started")
	}
	return runtime.NewInt64(int64(process.cmd.Process.Pid)), nil
}

// sshAuthFromOpts builds the ssh auth method list from the dict.
// Recognised keys: "password", "privateKey" (string PEM),
// "privateKeyFile" (path), "passphrase" (decrypts the key),
// "agent" (use SSH_AUTH_SOCK).
func sshAuthFromOpts(opts runtime.Dict) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if v, ok := dictField(opts, "password"); ok {
		s, ok := v.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("ssh: options.password must be string")
		}
		methods = append(methods, ssh.Password(s.Value))
	}
	loadKey := func(pem []byte) error {
		var signer ssh.Signer
		var err error
		if v, ok := dictField(opts, "passphrase"); ok {
			pass, ok := v.(runtime.String)
			if !ok {
				return fmt.Errorf("ssh: options.passphrase must be string")
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(pass.Value))
		} else {
			signer, err = ssh.ParsePrivateKey(pem)
		}
		if err != nil {
			return err
		}
		methods = append(methods, ssh.PublicKeys(signer))
		return nil
	}
	if v, ok := dictField(opts, "privateKey"); ok {
		s, ok := v.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("ssh: options.privateKey must be string")
		}
		if err := loadKey([]byte(s.Value)); err != nil {
			return nil, err
		}
	}
	if v, ok := dictField(opts, "privateKeyFile"); ok {
		s, ok := v.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("ssh: options.privateKeyFile must be string")
		}
		pem, err := os.ReadFile(s.Value)
		if err != nil {
			return nil, fmt.Errorf("ssh: read private key: %w", err)
		}
		if err := loadKey(pem); err != nil {
			return nil, err
		}
	}
	if v, ok := dictField(opts, "agent"); ok {
		b, ok := v.(runtime.Bool)
		if !ok {
			return nil, fmt.Errorf("ssh: options.agent must be bool")
		}
		if b.Value {
			sock := os.Getenv("SSH_AUTH_SOCK")
			if sock == "" {
				return nil, fmt.Errorf("ssh: agent requested but SSH_AUTH_SOCK is empty")
			}
			conn, err := net.Dial("unix", sock)
			if err != nil {
				return nil, fmt.Errorf("ssh: dial agent: %w", err)
			}
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("ssh: no authentication method supplied (set password / privateKey / privateKeyFile / agent)")
	}
	return methods, nil
}

func sshHostKeyCallbackFromOpts(opts runtime.Dict) (ssh.HostKeyCallback, error) {
	if v, ok := dictField(opts, "insecureSkipHostKey"); ok {
		b, ok := v.(runtime.Bool)
		if !ok {
			return nil, fmt.Errorf("ssh: options.insecureSkipHostKey must be bool")
		}
		if b.Value {
			return ssh.InsecureIgnoreHostKey(), nil
		}
	}
	if v, ok := dictField(opts, "knownHostsFile"); ok {
		s, ok := v.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("ssh: options.knownHostsFile must be string")
		}
		return knownhosts.New(s.Value)
	}
	// Default: read $HOME/.ssh/known_hosts if available, otherwise
	// fall back to InsecureIgnoreHostKey is wrong - require explicit
	// opt-in. Fail with a clear error.
	return nil, fmt.Errorf("ssh: host key verification not configured (set options.knownHostsFile or options.insecureSkipHostKey: true)")
}

// sshConnect dials an SSH server. target is "user@host" or just
// "host" (login from current user). Returns a dict with handle and
// remoteAddr.
func (e *Evaluator) sshConnect(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects (target, options?)", call.Callee.String())
	}
	target, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s target must be string", call.Callee.String())
	}
	var opts runtime.Dict
	if len(args) == 2 {
		opts, ok = args[1].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("%s options must be a dict", call.Callee.String())
		}
	} else {
		opts = runtime.Dict{Entries: map[string]runtime.DictEntry{}}
	}
	user := os.Getenv("USER")
	host := target.Value
	if at := strings.Index(target.Value, "@"); at >= 0 {
		user = target.Value[:at]
		host = target.Value[at+1:]
	}
	port := int64(22)
	if v, ok := dictField(opts, "port"); ok {
		n, ok := native.AsInt64(v)
		if !ok {
			return nil, fmt.Errorf("%s options.port must be int", call.Callee.String())
		}
		port = n
	}
	authMethods, err := sshAuthFromOpts(opts)
	if err != nil {
		return nil, err
	}
	hostKeyCb, err := sshHostKeyCallbackFromOpts(opts)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(0)
	if v, ok := dictField(opts, "timeoutMs"); ok {
		n, ok := native.AsInt64(v)
		if !ok {
			return nil, fmt.Errorf("%s options.timeoutMs must be int", call.Callee.String())
		}
		timeout = time.Duration(n) * time.Millisecond
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCb,
		Timeout:         timeout,
	}
	addr := net.JoinHostPort(host, strconv.FormatInt(port, 10))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	handle := &sshClientHandle{client: client}
	e.sshMu.Lock()
	e.nextSSHID++
	id := e.nextSSHID
	e.sshClients[id] = handle
	e.sshMu.Unlock()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "handle", runtime.NewInt64(id))
	putDict(entries, "user", runtime.String{Value: user})
	putDict(entries, "remoteAddr", runtime.String{Value: addr})
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) sshClient(value runtime.Value) (*sshClientHandle, error) {
	id, ok := native.AsInt64(value)
	if !ok {
		return nil, fmt.Errorf("ssh handle must be int")
	}
	e.sshMu.Lock()
	handle, ok := e.sshClients[id]
	e.sshMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.sshClient(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown ssh handle %d", id)
	}
	if handle.closed {
		return nil, fmt.Errorf("ssh handle %d is closed", id)
	}
	return handle, nil
}

func (e *Evaluator) sshExec(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (handle, command)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	cmd, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s command must be string", call.Callee.String())
	}
	session, err := handle.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	exitCode := int64(0)
	if err := session.Run(cmd.Value); err != nil {
		if ee, ok := err.(*ssh.ExitError); ok {
			exitCode = int64(ee.ExitStatus())
		} else {
			return nil, err
		}
	}
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "stdout", runtime.String{Value: stdout.String()})
	putDict(entries, "stderr", runtime.String{Value: stderr.String()})
	putDict(entries, "exitCode", runtime.NewInt64(exitCode))
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) sshSpawn(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects (handle, command, options?)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	cmd, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s command must be string", call.Callee.String())
	}
	session, err := handle.client.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.Start(cmd.Value); err != nil {
		_ = session.Close()
		return nil, err
	}
	sessionHandle := &sshSessionHandle{session: session, stdin: stdin, stdout: stdout, stderr: stderr}
	e.sshMu.Lock()
	e.nextSSHID++
	sid := e.nextSSHID
	e.sshSessions[sid] = sessionHandle
	e.sshMu.Unlock()
	stdinStream := e.registerIOStream(&ioStreamHandle{name: "ssh stdin", writer: stdin, closer: stdin})
	stdoutStream := e.registerIOStream(&ioStreamHandle{name: "ssh stdout", reader: stdout})
	stderrStream := e.registerIOStream(&ioStreamHandle{name: "ssh stderr", reader: stderr})
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "handle", runtime.NewInt64(sid))
	putDict(entries, "stdin", stdinStream)
	putDict(entries, "stdout", stdoutStream)
	putDict(entries, "stderr", stderrStream)
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) sshSession(value runtime.Value) (*sshSessionHandle, error) {
	id, ok := native.AsInt64(value)
	if !ok {
		return nil, fmt.Errorf("ssh session handle must be int")
	}
	e.sshMu.Lock()
	handle, ok := e.sshSessions[id]
	e.sshMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.sshSession(value)
	}
	if !ok {
		return nil, fmt.Errorf("unknown ssh session handle %d", id)
	}
	return handle, nil
}

func (e *Evaluator) sshSessionWait(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects (session handle)", call.Callee.String())
	}
	handle, err := e.sshSession(args[0])
	if err != nil {
		return nil, err
	}
	exitCode := int64(0)
	if err := handle.session.Wait(); err != nil {
		if ee, ok := err.(*ssh.ExitError); ok {
			exitCode = int64(ee.ExitStatus())
		} else {
			return nil, err
		}
	}
	return runtime.NewInt64(exitCode), nil
}

func (e *Evaluator) sshSessionKill(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("%s expects (session handle, signal?)", call.Callee.String())
	}
	handle, err := e.sshSession(args[0])
	if err != nil {
		return nil, err
	}
	sig := ssh.SIGKILL
	if len(args) == 2 {
		s, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s signal must be string", call.Callee.String())
		}
		sig = ssh.Signal(strings.TrimPrefix(s.Value, "SIG"))
	}
	return runtime.Null{}, handle.session.Signal(sig)
}

// sshSftp returns the cached sftp.Client for a handle, creating it
// on first use.
func sshSftp(handle *sshClientHandle) (*sftp.Client, error) {
	handle.sftpMu.Lock()
	defer handle.sftpMu.Unlock()
	if handle.sftpCli != nil {
		return handle.sftpCli, nil
	}
	cli, err := sftp.NewClient(handle.client)
	if err != nil {
		return nil, err
	}
	handle.sftpCli = cli
	return cli, nil
}

func (e *Evaluator) sshUpload(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects (handle, localPath, remotePath)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	localPath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s localPath must be string", call.Callee.String())
	}
	remotePath, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s remotePath must be string", call.Callee.String())
	}
	cli, err := sshSftp(handle)
	if err != nil {
		return nil, err
	}
	src, err := os.Open(localPath.Value)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	dst, err := cli.Create(remotePath.Value)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	written, err := io.Copy(dst, src)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(written), nil
}

func (e *Evaluator) sshDownload(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects (handle, remotePath, localPath)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	remotePath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s remotePath must be string", call.Callee.String())
	}
	localPath, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s localPath must be string", call.Callee.String())
	}
	cli, err := sshSftp(handle)
	if err != nil {
		return nil, err
	}
	src, err := cli.Open(remotePath.Value)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	dst, err := os.Create(localPath.Value)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	written, err := io.Copy(dst, src)
	if err != nil {
		return nil, err
	}
	return runtime.NewInt64(written), nil
}

func (e *Evaluator) sshSftpList(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (handle, remotePath)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	remotePath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s remotePath must be string", call.Callee.String())
	}
	cli, err := sshSftp(handle)
	if err != nil {
		return nil, err
	}
	infos, err := cli.ReadDir(remotePath.Value)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Value, 0, len(infos))
	for _, info := range infos {
		entries := map[string]runtime.DictEntry{}
		putDict(entries, "name", runtime.String{Value: info.Name()})
		putDict(entries, "size", runtime.NewInt64(info.Size()))
		putDict(entries, "mode", runtime.NewInt64(int64(info.Mode().Perm())))
		putDict(entries, "isDir", runtime.Bool{Value: info.IsDir()})
		putDict(entries, "modUnix", runtime.NewInt64(info.ModTime().Unix()))
		out = append(out, runtime.Dict{Entries: entries})
	}
	return &runtime.List{Elements: out}, nil
}

func (e *Evaluator) sshSftpRemove(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (handle, remotePath)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	remotePath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s remotePath must be string", call.Callee.String())
	}
	cli, err := sshSftp(handle)
	if err != nil {
		return nil, err
	}
	return runtime.Null{}, cli.Remove(remotePath.Value)
}

func (e *Evaluator) sshSftpMkdir(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects (handle, remotePath, mode?)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	remotePath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s remotePath must be string", call.Callee.String())
	}
	cli, err := sshSftp(handle)
	if err != nil {
		return nil, err
	}
	if err := cli.MkdirAll(remotePath.Value); err != nil {
		return nil, err
	}
	if len(args) == 3 {
		mode, ok := native.AsInt64(args[2])
		if !ok {
			return nil, fmt.Errorf("%s mode must be int", call.Callee.String())
		}
		if err := cli.Chmod(remotePath.Value, os.FileMode(mode)); err != nil {
			return nil, err
		}
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) sshSftpOpen(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("%s expects (handle, remotePath, mode?)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	remotePath, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s remotePath must be string", call.Callee.String())
	}
	mode := "r"
	if len(args) == 3 {
		s, ok := args[2].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s mode must be string", call.Callee.String())
		}
		mode = s.Value
	}
	cli, err := sshSftp(handle)
	if err != nil {
		return nil, err
	}
	var f *sftp.File
	switch mode {
	case "r":
		f, err = cli.Open(remotePath.Value)
	case "w":
		f, err = cli.Create(remotePath.Value)
	case "a":
		f, err = cli.OpenFile(remotePath.Value, os.O_WRONLY|os.O_APPEND|os.O_CREATE)
	default:
		return nil, fmt.Errorf("%s mode must be \"r\", \"w\", or \"a\"", call.Callee.String())
	}
	if err != nil {
		return nil, err
	}
	return e.registerIOStream(&ioStreamHandle{name: "ssh sftp file", reader: f, writer: f, closer: f}), nil
}

// sshForwardLocal binds localPort on 127.0.0.1 and forwards each
// accepted connection through the SSH server to remoteTarget
// ("host:port"). The returned tunnel handle can be used with
// tunnelClose to stop the accept-loop.
func (e *Evaluator) sshForwardLocal(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects (handle, localPort, remoteTarget)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	localPort, ok := native.AsInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s localPort must be int", call.Callee.String())
	}
	remote, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s remoteTarget must be string", call.Callee.String())
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.FormatInt(localPort, 10)))
	if err != nil {
		return nil, err
	}
	tunnel := &sshTunnelHandle{listener: listener}
	e.sshMu.Lock()
	e.nextSSHID++
	tid := e.nextSSHID
	e.sshTunnels[tid] = tunnel
	e.sshMu.Unlock()
	tunnel.wg.Add(1)
	go func() {
		defer tunnel.wg.Done()
		for {
			local, err := listener.Accept()
			if err != nil {
				return
			}
			go func(local net.Conn) {
				defer local.Close()
				remoteConn, err := handle.client.Dial("tcp", remote.Value)
				if err != nil {
					return
				}
				defer remoteConn.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(remoteConn, local); done <- struct{}{} }()
				go func() { _, _ = io.Copy(local, remoteConn); done <- struct{}{} }()
				<-done
			}(local)
		}
	}()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "handle", runtime.NewInt64(tid))
	putDict(entries, "localAddr", runtime.String{Value: listener.Addr().String()})
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) sshForwardRemote(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s expects (handle, remotePort, localTarget)", call.Callee.String())
	}
	handle, err := e.sshClient(args[0])
	if err != nil {
		return nil, err
	}
	remotePort, ok := native.AsInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("%s remotePort must be int", call.Callee.String())
	}
	local, ok := args[2].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s localTarget must be string", call.Callee.String())
	}
	listener, err := handle.client.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.FormatInt(remotePort, 10)))
	if err != nil {
		return nil, err
	}
	tunnel := &sshTunnelHandle{listener: listener}
	e.sshMu.Lock()
	e.nextSSHID++
	tid := e.nextSSHID
	e.sshTunnels[tid] = tunnel
	e.sshMu.Unlock()
	tunnel.wg.Add(1)
	go func() {
		defer tunnel.wg.Done()
		for {
			remoteConn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(remote net.Conn) {
				defer remote.Close()
				localConn, err := net.Dial("tcp", local.Value)
				if err != nil {
					return
				}
				defer localConn.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(localConn, remote); done <- struct{}{} }()
				go func() { _, _ = io.Copy(remote, localConn); done <- struct{}{} }()
				<-done
			}(remoteConn)
		}
	}()
	entries := map[string]runtime.DictEntry{}
	putDict(entries, "handle", runtime.NewInt64(tid))
	putDict(entries, "remoteAddr", runtime.String{Value: listener.Addr().String()})
	return runtime.Dict{Entries: entries}, nil
}

func (e *Evaluator) sshTunnelClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects (tunnel handle)", call.Callee.String())
	}
	id, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s tunnel handle must be int", call.Callee.String())
	}
	e.sshMu.Lock()
	tunnel, ok := e.sshTunnels[id]
	if ok {
		delete(e.sshTunnels, id)
	}
	e.sshMu.Unlock()
	if !ok {
		if e.parent != nil {
			return e.parent.sshTunnelClose(call, args)
		}
		return runtime.Null{}, nil
	}
	return runtime.Null{}, stopSSHTunnelHandle(tunnel)
}

func stopSSHTunnelHandle(tunnel *sshTunnelHandle) error {
	if tunnel == nil || tunnel.stopped {
		return nil
	}
	tunnel.stopped = true
	err := tunnel.listener.Close()
	tunnel.wg.Wait()
	if err != nil &&
		!errors.Is(err, net.ErrClosed) &&
		!strings.Contains(err.Error(), "use of closed network connection") {
		return err
	}
	return nil
}

func (e *Evaluator) sshClose(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects (handle)", call.Callee.String())
	}
	id, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s handle must be int", call.Callee.String())
	}
	e.sshMu.Lock()
	handle, ok := e.sshClients[id]
	if ok {
		delete(e.sshClients, id)
	}
	e.sshMu.Unlock()
	if !ok {
		if e.parent != nil {
			return e.parent.sshClose(call, args)
		}
		return runtime.Null{}, nil
	}
	return runtime.Null{}, closeSSHClientHandle(handle)
}

func closeSSHClientHandle(handle *sshClientHandle) error {
	if handle == nil || handle.closed {
		return nil
	}
	handle.closed = true
	if handle.sftpCli != nil {
		_ = handle.sftpCli.Close()
	}
	if err := handle.client.Close(); err != nil &&
		!errors.Is(err, net.ErrClosed) &&
		!strings.Contains(err.Error(), "use of closed network connection") {
		return err
	}
	return nil
}

func sysSetenv(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects exactly two arguments", call.Callee.String())
	}
	name, ok := args[0].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s name must be string", call.Callee.String())
	}
	value, ok := args[1].(runtime.String)
	if !ok {
		return nil, fmt.Errorf("%s value must be string", call.Callee.String())
	}
	if err := os.Setenv(name.Value, value.Value); err != nil {
		return nil, err
	}
	return runtime.Null{}, nil
}

func sysSleep(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one argument (milliseconds)", call.Callee.String())
	}
	if v, ok := args[0].(runtime.Float); ok {
		if v.Value > 0 {
			time.Sleep(time.Duration(v.Value * float64(time.Millisecond)))
		}
		return runtime.Null{}, nil
	}
	ms, ok := native.AsInt64(args[0])
	if !ok {
		return nil, fmt.Errorf("%s expects a numeric millisecond value", call.Callee.String())
	}
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) asyncRun(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one function argument", call.Callee.String())
	}
	fn, ok := args[0].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("%s expects a function", call.Callee.String())
	}
	return e.startAsyncFunction(fn, nil, nil), nil
}

// asyncToken returns a fresh uncompleted Task whose only purpose is
// to carry a cancellation signal. Lets concurrent code share a
// cancellation point without mutating instance fields across
// goroutines.
func asyncToken(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("%s takes no arguments", call.Callee.String())
	}
	return runtime.NewTask(), nil
}

func asyncSleep(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one argument (milliseconds)", call.Callee.String())
	}
	duration, err := sleepDuration(args[0], call.Callee.String())
	if err != nil {
		return nil, err
	}
	task := runtime.NewTask()
	go func() {
		if duration > 0 {
			time.Sleep(duration)
		}
		task.Complete(runtime.Null{}, nil)
	}()
	return task, nil
}

func asyncAwait(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one task", call.Callee.String())
	}
	return awaitValue(args[0])
}

func asyncDone(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one task", call.Callee.String())
	}
	task, ok := args[0].(*runtime.Task)
	if !ok {
		return nil, fmt.Errorf("%s expects Task", call.Callee.String())
	}
	return runtime.Bool{Value: task.Done()}, nil
}

func asyncCancel(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one task", call.Callee.String())
	}
	task, ok := args[0].(*runtime.Task)
	if !ok {
		return nil, fmt.Errorf("%s expects Task", call.Callee.String())
	}
	task.Cancel()
	return runtime.Null{}, nil
}

func asyncTimeout(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects (task, milliseconds)", call.Callee.String())
	}
	inner, ok := args[0].(*runtime.Task)
	if !ok {
		return nil, fmt.Errorf("%s expects a Task as the first argument", call.Callee.String())
	}
	duration, err := sleepDuration(args[1], call.Callee.String())
	if err != nil {
		return nil, err
	}
	out := runtime.NewTask()
	go func() {
		select {
		case <-inner.DoneChan():
			result := inner.Await()
			out.Complete(result.Value, result.Err)
		case <-time.After(duration):
			inner.Cancel()
			out.Complete(runtime.Null{}, fmt.Errorf("async.timeout: task did not complete within %v", duration))
		}
	}()
	return out, nil
}

func asyncAll(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one list of tasks", call.Callee.String())
	}
	list, ok := args[0].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s expects a list of tasks", call.Callee.String())
	}
	tasks := make([]*runtime.Task, 0, len(list.Elements))
	for i, el := range list.Elements {
		task, ok := el.(*runtime.Task)
		if !ok {
			return nil, fmt.Errorf("%s: element %d is not a Task", call.Callee.String(), i)
		}
		tasks = append(tasks, task)
	}
	out := runtime.NewTask()
	go func() {
		results := make([]runtime.Value, len(tasks))
		for i, t := range tasks {
			r := t.Await()
			if r.Err != nil {
				// Cancel all siblings so they stop wasting work.
				for j, sibling := range tasks {
					if j != i {
						sibling.Cancel()
					}
				}
				out.Complete(runtime.Null{}, r.Err)
				return
			}
			results[i] = r.Value
		}
		out.Complete(&runtime.List{Elements: results}, nil)
	}()
	return out, nil
}

func asyncRace(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one list of tasks", call.Callee.String())
	}
	list, ok := args[0].(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s expects a list of tasks", call.Callee.String())
	}
	if len(list.Elements) == 0 {
		return nil, fmt.Errorf("%s requires at least one task", call.Callee.String())
	}
	tasks := make([]*runtime.Task, 0, len(list.Elements))
	for i, el := range list.Elements {
		task, ok := el.(*runtime.Task)
		if !ok {
			return nil, fmt.Errorf("%s: element %d is not a Task", call.Callee.String(), i)
		}
		tasks = append(tasks, task)
	}
	out := runtime.NewTask()
	go func() {
		// Race all DoneChans; whichever fires first wins.
		winner := make(chan int, len(tasks))
		for i, t := range tasks {
			i, t := i, t
			go func() {
				<-t.DoneChan()
				winner <- i
			}()
		}
		first := <-winner
		for j, sibling := range tasks {
			if j != first {
				sibling.Cancel()
			}
		}
		r := tasks[first].Await()
		out.Complete(r.Value, r.Err)
	}()
	return out, nil
}

func sleepDuration(value runtime.Value, label string) (time.Duration, error) {
	switch v := value.(type) {
	case runtime.SmallInt:
		if v.Value <= 0 {
			return 0, nil
		}
		return time.Duration(v.Value) * time.Millisecond, nil
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, fmt.Errorf("%s millisecond value is out of int64 range", label)
		}
		ms := v.Value.Int64()
		if ms <= 0 {
			return 0, nil
		}
		return time.Duration(ms) * time.Millisecond, nil
	case runtime.Float:
		if v.Value <= 0 {
			return 0, nil
		}
		return time.Duration(v.Value * float64(time.Millisecond)), nil
	default:
		return 0, fmt.Errorf("%s expects a numeric millisecond value", label)
	}
}

func (e *Evaluator) jsonStream(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects source and handler", call.Callee.String())
	}
	handler, ok := args[1].(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("%s handler must be an object implementing JsonStreamInterface", call.Callee.String())
	}
	readerValue, err := e.jsonReader(call, []runtime.Value{args[0]})
	if err != nil {
		return nil, err
	}
	reader, ok := readerValue.(runtime.NativeObject)
	if !ok {
		return nil, fmt.Errorf("internal error: json reader returned unexpected type")
	}
	defer e.closeJSONReader(reader.ID)
	count := int64(0)
	for {
		stream, err := e.lookupJSONReader(reader.ID)
		if err != nil {
			return nil, err
		}
		ok, err := e.jsonReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		event := stream.pending
		stream.pending = nil
		count++
		stop, err := e.dispatchJSONStreamEvent(handler, event)
		if err != nil {
			return nil, err
		}
		if stop {
			break
		}
	}
	return runtime.NewInt64(count), nil
}

func (e *Evaluator) csvStream(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects source and handler", call.Callee.String())
	}
	handler, ok := args[1].(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("%s handler must be an object implementing CsvStreamInterface", call.Callee.String())
	}
	readerValue, err := e.csvReader(call, []runtime.Value{args[0]})
	if err != nil {
		return nil, err
	}
	reader, ok := readerValue.(runtime.NativeObject)
	if !ok {
		return nil, fmt.Errorf("internal error: csv reader returned unexpected type")
	}
	defer e.closeCSVReader(reader.ID)
	count := int64(0)
	for {
		stream, err := e.lookupCSVReader(reader.ID)
		if err != nil {
			return nil, err
		}
		ok, err := e.csvReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		event := stream.pending
		stream.pending = nil
		count++
		stop, err := e.dispatchCSVStreamEvent(handler, event)
		if err != nil {
			return nil, err
		}
		if stop {
			break
		}
	}
	return runtime.NewInt64(count), nil
}

func (e *Evaluator) yamlStream(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects source and handler", call.Callee.String())
	}
	handler, ok := args[1].(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("%s handler must be an object implementing YamlStreamInterface", call.Callee.String())
	}
	readerValue, err := e.yamlReader(call, []runtime.Value{args[0]})
	if err != nil {
		return nil, err
	}
	reader, ok := readerValue.(runtime.NativeObject)
	if !ok {
		return nil, fmt.Errorf("internal error: yaml reader returned unexpected type")
	}
	defer e.closeYAMLReader(reader.ID)
	count := int64(0)
	for {
		stream, err := e.lookupYAMLReader(reader.ID)
		if err != nil {
			return nil, err
		}
		ok, err := e.yamlReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		event := stream.pending
		stream.pending = nil
		count++
		stop, err := e.dispatchYAMLStreamEvent(handler, event)
		if err != nil {
			return nil, err
		}
		if stop {
			break
		}
	}
	return runtime.NewInt64(count), nil
}

func (e *Evaluator) yamlReader(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	source, err := e.streamSourceReader(call, args[0])
	if err != nil {
		return nil, err
	}
	decoder := yamllib.NewDecoder(source.Reader)
	e.yamlMu.Lock()
	defer e.yamlMu.Unlock()
	e.nextYAMLID++
	id := e.nextYAMLID
	e.yamlReaders[id] = &yamlStreamReader{decoder: decoder}
	return runtime.NativeObject{Kind: "YamlReader", ID: id}, nil
}

func (e *Evaluator) closeYAMLReader(id int64) {
	e.yamlMu.Lock()
	delete(e.yamlReaders, id)
	e.yamlMu.Unlock()
}

func (e *Evaluator) yamlReaderMethod(reader runtime.NativeObject, name string, args []runtime.Value) (runtime.Value, error) {
	if reader.Kind != "YamlReader" {
		return nil, fmt.Errorf("%s has no method %s", reader.TypeName(), name)
	}
	if name == "close" {
		if len(args) != 0 {
			return nil, fmt.Errorf("YamlReader.close expects no arguments")
		}
		e.closeYAMLReader(reader.ID)
		return runtime.Null{}, nil
	}
	if len(args) != 0 {
		return nil, fmt.Errorf("YamlReader.%s expects no arguments", name)
	}
	stream, err := e.lookupYAMLReader(reader.ID)
	if err != nil {
		return nil, err
	}
	switch name {
	case "hasNext":
		ok, err := e.yamlReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: ok}, nil
	case "next":
		ok, err := e.yamlReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			return runtime.Null{}, nil
		}
		event := stream.pending
		stream.pending = nil
		return event, nil
	default:
		return nil, fmt.Errorf("YamlReader has no method %s", name)
	}
}

func (e *Evaluator) lookupYAMLReader(id int64) (*yamlStreamReader, error) {
	e.yamlMu.Lock()
	reader, ok := e.yamlReaders[id]
	e.yamlMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.lookupYAMLReader(id)
	}
	if !ok {
		return nil, fmt.Errorf("YamlReader is closed")
	}
	return reader, nil
}

func (e *Evaluator) yamlReaderHasNext(reader *yamlStreamReader) (bool, error) {
	if reader.pending != nil {
		return true, nil
	}
	if reader.done {
		return false, nil
	}
	event := nextYAMLEvent(reader)
	if event == nil {
		reader.done = true
		return false, nil
	}
	reader.pending = event
	return true, nil
}

func nextYAMLEvent(reader *yamlStreamReader) runtime.Value {
	for len(reader.queue) == 0 {
		var node yamllib.Node
		if err := reader.decoder.Decode(&node); err == io.EOF {
			return nil
		} else if err != nil {
			reader.done = true
			return yamlEvent("error", native.ParseErrorValue(yamlParseError(err)))
		}
		enqueueYAMLEvents(reader, &node)
	}
	event := reader.queue[0]
	reader.queue[0] = nil
	reader.queue = reader.queue[1:]
	return event
}

func enqueueYAMLEvents(reader *yamlStreamReader, node *yamllib.Node) {
	enqueueYAMLEventsVisited(reader, node, map[*yamllib.Node]bool{})
}

func enqueueYAMLEventsVisited(reader *yamlStreamReader, node *yamllib.Node, visited map[*yamllib.Node]bool) {
	if reader.done {
		return
	}
	if node == nil {
		reader.queue = append(reader.queue, yamlEvent("value", runtime.Null{}))
		return
	}
	if node.Kind == yamllib.DocumentNode {
		if len(node.Content) == 0 {
			return
		}
		enqueueYAMLEventsVisited(reader, node.Content[0], visited)
		return
	}
	switch node.Kind {
	case yamllib.MappingNode:
		reader.queue = append(reader.queue, yamlEvent("startMap", runtime.Null{}))
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := yamlKeyText(node.Content[i])
			reader.queue = append(reader.queue, yamlEvent("key", runtime.String{Value: key}))
			enqueueYAMLEventsVisited(reader, node.Content[i+1], visited)
		}
		reader.queue = append(reader.queue, yamlEvent("endMap", runtime.Null{}))
	case yamllib.SequenceNode:
		reader.queue = append(reader.queue, yamlEvent("startList", runtime.Null{}))
		for _, item := range node.Content {
			enqueueYAMLEventsVisited(reader, item, visited)
		}
		reader.queue = append(reader.queue, yamlEvent("endList", runtime.Null{}))
	case yamllib.ScalarNode:
		reader.queue = append(reader.queue, yamlEvent("value", yamlScalarValue(node)))
	case yamllib.AliasNode:
		if visited[node.Alias] {
			parseErr := native.NewParseError("yaml: alias cycle detected", "", -1)
			reader.queue = append(reader.queue, yamlEvent("error", native.ParseErrorValue(parseErr)))
			reader.done = true
			return
		}
		visited[node.Alias] = true
		enqueueYAMLEventsVisited(reader, node.Alias, visited)
	default:
		reader.queue = append(reader.queue, yamlEvent("value", runtime.Null{}))
	}
}

func yamlKeyText(node *yamllib.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind == yamllib.ScalarNode {
		return node.Value
	}
	value := yamlScalarValue(node)
	return value.Inspect()
}

func yamlScalarValue(node *yamllib.Node) runtime.Value {
	switch node.Tag {
	case "!!null":
		return runtime.Null{}
	case "!!bool":
		return runtime.Bool{Value: strings.EqualFold(node.Value, "true")}
	case "!!int":
		value, err := runtime.NewIntLiteral(strings.ReplaceAll(node.Value, "_", ""))
		if err == nil {
			return value
		}
		// Malformed !!int literal (e.g. non-numeric YAML tag): fall through to string.
	case "!!float":
		value, err := strconv.ParseFloat(strings.ReplaceAll(node.Value, "_", ""), 64)
		if err == nil {
			return runtime.Float{Value: value}
		}
		// Malformed !!float literal: fall through to string.
	}
	return runtime.String{Value: node.Value}
}

func yamlParseError(err error) native.ParseError {
	parseErr := native.NewParseError(err.Error(), "", -1)
	if yamlErr, ok := err.(*yamllib.TypeError); ok && len(yamlErr.Errors) > 0 {
		parseErr.Message = strings.Join(yamlErr.Errors, "; ")
	}
	return parseErr
}

func yamlEvent(kind string, value runtime.Value) runtime.Value {
	return runtime.Dict{Entries: map[string]runtime.DictEntry{
		dictKey(runtime.String{Value: "type"}):  {Key: runtime.String{Value: "type"}, Value: runtime.String{Value: kind}},
		dictKey(runtime.String{Value: "value"}): {Key: runtime.String{Value: "value"}, Value: value},
	}}
}

func (e *Evaluator) dispatchYAMLStreamEvent(handler *runtime.Instance, event runtime.Value) (bool, error) {
	eventDict, ok := event.(runtime.Dict)
	if !ok {
		return false, fmt.Errorf("YAML stream event must be dict")
	}
	eventType, ok := dictStringField(eventDict, "type")
	if !ok {
		return false, fmt.Errorf("YAML stream event is missing type")
	}
	value, _ := dictField(eventDict, "value")
	switch eventType {
	case "startMap":
		return false, e.callStreamHandler(handler, "onStartMap", nil)
	case "endMap":
		return false, e.callStreamHandler(handler, "onEndMap", nil)
	case "startList":
		return false, e.callStreamHandler(handler, "onStartList", nil)
	case "endList":
		return false, e.callStreamHandler(handler, "onEndList", nil)
	case "key":
		return false, e.callStreamHandler(handler, "onKey", []runtime.Value{value})
	case "value":
		return false, e.callStreamHandler(handler, "onValue", []runtime.Value{value})
	case "error":
		return true, e.callStreamHandler(handler, "onError", []runtime.Value{value})
	default:
		return false, fmt.Errorf("unknown YAML stream event %q", eventType)
	}
}

func (e *Evaluator) csvReader(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	source, err := e.streamSourceReader(call, args[0])
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(source.Reader)
	e.csvMu.Lock()
	defer e.csvMu.Unlock()
	e.nextCSVID++
	id := e.nextCSVID
	e.csvReaders[id] = &csvStreamReader{reader: reader}
	return runtime.NativeObject{Kind: "CsvReader", ID: id}, nil
}

func (e *Evaluator) closeCSVReader(id int64) {
	e.csvMu.Lock()
	delete(e.csvReaders, id)
	e.csvMu.Unlock()
}

func (e *Evaluator) csvReaderMethod(reader runtime.NativeObject, name string, args []runtime.Value) (runtime.Value, error) {
	if reader.Kind != "CsvReader" {
		return nil, fmt.Errorf("%s has no method %s", reader.TypeName(), name)
	}
	if name == "close" {
		if len(args) != 0 {
			return nil, fmt.Errorf("CsvReader.close expects no arguments")
		}
		e.closeCSVReader(reader.ID)
		return runtime.Null{}, nil
	}
	if len(args) != 0 {
		return nil, fmt.Errorf("CsvReader.%s expects no arguments", name)
	}
	stream, err := e.lookupCSVReader(reader.ID)
	if err != nil {
		return nil, err
	}
	switch name {
	case "hasNext":
		ok, err := e.csvReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: ok}, nil
	case "next":
		ok, err := e.csvReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			return runtime.Null{}, nil
		}
		event := stream.pending
		stream.pending = nil
		return event, nil
	default:
		return nil, fmt.Errorf("CsvReader has no method %s", name)
	}
}

func (e *Evaluator) lookupCSVReader(id int64) (*csvStreamReader, error) {
	e.csvMu.Lock()
	reader, ok := e.csvReaders[id]
	e.csvMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.lookupCSVReader(id)
	}
	if !ok {
		return nil, fmt.Errorf("CsvReader is closed")
	}
	return reader, nil
}

func (e *Evaluator) csvReaderHasNext(reader *csvStreamReader) (bool, error) {
	if reader.pending != nil {
		return true, nil
	}
	if reader.done {
		return false, nil
	}
	event := nextCSVEvent(reader)
	if event == nil {
		reader.done = true
		return false, nil
	}
	reader.pending = event
	return true, nil
}

func nextCSVEvent(reader *csvStreamReader) runtime.Value {
	record, err := reader.reader.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		reader.done = true
		return csvEvent("error", native.ParseErrorValue(csvParseError(err)))
	}
	reader.row++
	if reader.row == 1 {
		return csvEvent("header", stringListValue(record))
	}
	return csvEvent("row", stringListValue(record))
}

func csvParseError(err error) native.ParseError {
	parseErr := native.NewParseError(err.Error(), "", -1)
	if csvErr, ok := err.(*csv.ParseError); ok {
		parseErr.Line = int64(csvErr.Line)
		parseErr.Column = int64(csvErr.Column)
	}
	return parseErr
}

func csvEvent(kind string, value runtime.Value) runtime.Value {
	return runtime.Dict{Entries: map[string]runtime.DictEntry{
		dictKey(runtime.String{Value: "type"}):  {Key: runtime.String{Value: "type"}, Value: runtime.String{Value: kind}},
		dictKey(runtime.String{Value: "value"}): {Key: runtime.String{Value: "value"}, Value: value},
	}}
}

func stringListValue(values []string) runtime.Value {
	elements := make([]runtime.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, runtime.String{Value: value})
	}
	return &runtime.List{Elements: elements}
}

func (e *Evaluator) dispatchCSVStreamEvent(handler *runtime.Instance, event runtime.Value) (bool, error) {
	eventDict, ok := event.(runtime.Dict)
	if !ok {
		return false, fmt.Errorf("CSV stream event must be dict")
	}
	eventType, ok := dictStringField(eventDict, "type")
	if !ok {
		return false, fmt.Errorf("CSV stream event is missing type")
	}
	value, _ := dictField(eventDict, "value")
	switch eventType {
	case "header":
		return false, e.callStreamHandler(handler, "onHeader", []runtime.Value{value})
	case "row":
		return false, e.callStreamHandler(handler, "onRow", []runtime.Value{value})
	case "error":
		return true, e.callStreamHandler(handler, "onError", []runtime.Value{value})
	default:
		return false, fmt.Errorf("unknown CSV stream event %q", eventType)
	}
}

func (e *Evaluator) xmlStream(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s expects source and handler", call.Callee.String())
	}
	handler, ok := args[1].(*runtime.Instance)
	if !ok {
		return nil, fmt.Errorf("%s handler must be an object implementing XmlStreamInterface", call.Callee.String())
	}
	readerValue, err := e.xmlReader(call, []runtime.Value{args[0]})
	if err != nil {
		return nil, err
	}
	reader, ok := readerValue.(runtime.NativeObject)
	if !ok {
		return nil, fmt.Errorf("internal error: xml reader returned unexpected type")
	}
	defer e.closeXMLReader(reader.ID)
	count := int64(0)
	for {
		stream, err := e.lookupXMLReader(reader.ID)
		if err != nil {
			return nil, err
		}
		ok, err := e.xmlReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		event := stream.pending
		stream.pending = nil
		count++
		stop, err := e.dispatchXMLStreamEvent(handler, event)
		if err != nil {
			return nil, err
		}
		if stop {
			break
		}
	}
	return runtime.NewInt64(count), nil
}

func (e *Evaluator) xmlReader(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	source, err := e.streamSourceReader(call, args[0])
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(source.Reader)
	e.xmlMu.Lock()
	defer e.xmlMu.Unlock()
	e.nextXMLID++
	id := e.nextXMLID
	e.xmlReaders[id] = &xmlStreamReader{decoder: decoder, source: source.Text}
	return runtime.NativeObject{Kind: "XmlReader", ID: id}, nil
}

func (e *Evaluator) closeXMLReader(id int64) {
	e.xmlMu.Lock()
	delete(e.xmlReaders, id)
	e.xmlMu.Unlock()
}

func (e *Evaluator) xmlReaderMethod(reader runtime.NativeObject, name string, args []runtime.Value) (runtime.Value, error) {
	if reader.Kind != "XmlReader" {
		return nil, fmt.Errorf("%s has no method %s", reader.TypeName(), name)
	}
	if name == "close" {
		if len(args) != 0 {
			return nil, fmt.Errorf("XmlReader.close expects no arguments")
		}
		e.closeXMLReader(reader.ID)
		return runtime.Null{}, nil
	}
	if len(args) != 0 {
		return nil, fmt.Errorf("XmlReader.%s expects no arguments", name)
	}
	stream, err := e.lookupXMLReader(reader.ID)
	if err != nil {
		return nil, err
	}
	switch name {
	case "hasNext":
		ok, err := e.xmlReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: ok}, nil
	case "next":
		ok, err := e.xmlReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			return runtime.Null{}, nil
		}
		event := stream.pending
		stream.pending = nil
		return event, nil
	default:
		return nil, fmt.Errorf("XmlReader has no method %s", name)
	}
}

func (e *Evaluator) dispatchXMLStreamEvent(handler *runtime.Instance, event runtime.Value) (bool, error) {
	eventDict, ok := event.(runtime.Dict)
	if !ok {
		return false, fmt.Errorf("XML stream event must be dict")
	}
	eventType, ok := dictStringField(eventDict, "type")
	if !ok {
		return false, fmt.Errorf("XML stream event is missing type")
	}
	value, _ := dictField(eventDict, "value")
	switch eventType {
	case "startElement":
		element, ok := value.(runtime.Dict)
		if !ok {
			return false, fmt.Errorf("XML startElement value must be dict")
		}
		name, ok := dictField(element, "name")
		if !ok {
			return false, fmt.Errorf("XML startElement value is missing name")
		}
		attributes, ok := dictField(element, "attributes")
		if !ok {
			return false, fmt.Errorf("XML startElement value is missing attributes")
		}
		return false, e.callStreamHandler(handler, "onStartElement", []runtime.Value{name, attributes})
	case "endElement":
		return false, e.callStreamHandler(handler, "onEndElement", []runtime.Value{value})
	case "text":
		return false, e.callStreamHandler(handler, "onText", []runtime.Value{value})
	case "comment":
		return false, e.callStreamHandler(handler, "onComment", []runtime.Value{value})
	case "error":
		return true, e.callStreamHandler(handler, "onError", []runtime.Value{value})
	default:
		return false, fmt.Errorf("unknown XML stream event %q", eventType)
	}
}

func (e *Evaluator) lookupXMLReader(id int64) (*xmlStreamReader, error) {
	e.xmlMu.Lock()
	reader, ok := e.xmlReaders[id]
	e.xmlMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.lookupXMLReader(id)
	}
	if !ok {
		return nil, fmt.Errorf("XmlReader is closed")
	}
	return reader, nil
}

func (e *Evaluator) xmlReaderHasNext(reader *xmlStreamReader) (bool, error) {
	if reader.pending != nil {
		return true, nil
	}
	if reader.done {
		return false, nil
	}
	event := nextXMLEvent(reader)
	if event == nil {
		reader.done = true
		return false, nil
	}
	reader.pending = event
	return true, nil
}

func nextXMLEvent(reader *xmlStreamReader) runtime.Value {
	for {
		tokenValue, err := reader.decoder.Token()
		if err == io.EOF {
			if reader.roots == 1 && reader.depth == 0 {
				return nil
			}
			reader.done = true
			parseErr := native.NewParseError("XML document must contain exactly one root element", reader.source, reader.decoder.InputOffset())
			return xmlEvent("error", native.ParseErrorValue(parseErr))
		}
		if err != nil {
			reader.done = true
			parseErr := native.NewParseError(err.Error(), reader.source, reader.decoder.InputOffset())
			if syntaxErr, ok := err.(*xml.SyntaxError); ok && syntaxErr.Line > 0 {
				parseErr.Line = int64(syntaxErr.Line)
			}
			return xmlEvent("error", native.ParseErrorValue(parseErr))
		}
		switch tok := tokenValue.(type) {
		case xml.StartElement:
			if reader.depth == 0 {
				reader.roots++
				if reader.roots > 1 {
					reader.done = true
					parseErr := native.NewParseError("XML document must contain exactly one root element", reader.source, reader.decoder.InputOffset())
					return xmlEvent("error", native.ParseErrorValue(parseErr))
				}
			}
			reader.depth++
			return xmlEvent("startElement", xmlStartElementValue(tok))
		case xml.EndElement:
			reader.depth--
			if reader.depth < 0 {
				reader.done = true
				parseErr := native.NewParseError(fmt.Sprintf("unexpected XML end element %s", tok.Name.Local), reader.source, reader.decoder.InputOffset())
				return xmlEvent("error", native.ParseErrorValue(parseErr))
			}
			return xmlEvent("endElement", runtime.String{Value: tok.Name.Local})
		case xml.CharData:
			text := string(tok)
			if reader.depth == 0 {
				if strings.TrimSpace(text) == "" {
					continue
				}
				reader.done = true
				parseErr := native.NewParseError("XML document contains non-whitespace text outside the root element", reader.source, reader.decoder.InputOffset())
				return xmlEvent("error", native.ParseErrorValue(parseErr))
			}
			if text == "" {
				continue
			}
			return xmlEvent("text", runtime.String{Value: text})
		case xml.Comment:
			return xmlEvent("comment", runtime.String{Value: string(tok)})
		}
	}
}

func xmlStartElementValue(element xml.StartElement) runtime.Value {
	attrs := map[string]runtime.DictEntry{}
	for _, attr := range element.Attr {
		key := runtime.String{Value: attr.Name.Local}
		attrs[dictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.String{Value: attr.Value}}
	}
	return runtime.Dict{Entries: map[string]runtime.DictEntry{
		dictKey(runtime.String{Value: "name"}):       {Key: runtime.String{Value: "name"}, Value: runtime.String{Value: element.Name.Local}},
		dictKey(runtime.String{Value: "attributes"}): {Key: runtime.String{Value: "attributes"}, Value: runtime.Dict{Entries: attrs}},
	}}
}

func xmlEvent(kind string, value runtime.Value) runtime.Value {
	return runtime.Dict{Entries: map[string]runtime.DictEntry{
		dictKey(runtime.String{Value: "type"}):  {Key: runtime.String{Value: "type"}, Value: runtime.String{Value: kind}},
		dictKey(runtime.String{Value: "value"}): {Key: runtime.String{Value: "value"}, Value: value},
	}}
}

func (e *Evaluator) jsonReader(call *ast.CallExpression, args []runtime.Value) (runtime.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	source, err := e.streamSourceReader(call, args[0])
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(source.Reader)
	decoder.UseNumber()
	e.jsonMu.Lock()
	defer e.jsonMu.Unlock()
	e.nextJSONID++
	id := e.nextJSONID
	e.jsonReaders[id] = &jsonStreamReader{decoder: decoder}
	return runtime.NativeObject{Kind: "JsonReader", ID: id}, nil
}

func (e *Evaluator) closeJSONReader(id int64) {
	e.jsonMu.Lock()
	delete(e.jsonReaders, id)
	e.jsonMu.Unlock()
}

func (e *Evaluator) jsonReaderMethod(reader runtime.NativeObject, name string, args []runtime.Value) (runtime.Value, error) {
	if reader.Kind != "JsonReader" {
		return nil, fmt.Errorf("%s has no method %s", reader.TypeName(), name)
	}
	if name == "close" {
		if len(args) != 0 {
			return nil, fmt.Errorf("JsonReader.close expects no arguments")
		}
		e.closeJSONReader(reader.ID)
		return runtime.Null{}, nil
	}
	if len(args) != 0 {
		return nil, fmt.Errorf("JsonReader.%s expects no arguments", name)
	}
	stream, err := e.lookupJSONReader(reader.ID)
	if err != nil {
		return nil, err
	}
	switch name {
	case "hasNext":
		ok, err := e.jsonReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: ok}, nil
	case "next":
		ok, err := e.jsonReaderHasNext(stream)
		if err != nil {
			return nil, err
		}
		if !ok {
			return runtime.Null{}, nil
		}
		event := stream.pending
		stream.pending = nil
		return event, nil
	default:
		return nil, fmt.Errorf("JsonReader has no method %s", name)
	}
}

func (e *Evaluator) dispatchJSONStreamEvent(handler *runtime.Instance, event runtime.Value) (bool, error) {
	eventDict, ok := event.(runtime.Dict)
	if !ok {
		return false, fmt.Errorf("JSON stream event must be dict")
	}
	eventType, ok := dictStringField(eventDict, "type")
	if !ok {
		return false, fmt.Errorf("JSON stream event is missing type")
	}
	value, _ := dictField(eventDict, "value")
	switch eventType {
	case "startObject":
		return false, e.callStreamHandler(handler, "onStartObject", nil)
	case "endObject":
		return false, e.callStreamHandler(handler, "onEndObject", nil)
	case "startArray":
		return false, e.callStreamHandler(handler, "onStartArray", nil)
	case "endArray":
		return false, e.callStreamHandler(handler, "onEndArray", nil)
	case "key":
		return false, e.callStreamHandler(handler, "onKey", []runtime.Value{value})
	case "value":
		return false, e.callStreamHandler(handler, "onValue", []runtime.Value{value})
	case "error":
		return true, e.callStreamHandler(handler, "onError", []runtime.Value{value})
	default:
		return false, fmt.Errorf("unknown JSON stream event %q", eventType)
	}
}

func (e *Evaluator) callStreamHandler(handler *runtime.Instance, name string, args []runtime.Value) error {
	methods := lookupMethodOverloads(handler.Class, name)
	if len(methods) == 0 {
		return fmt.Errorf("%s does not implement %s", handler.Class.Name, name)
	}
	method, err := selectOverload(handler.Class.Name+"."+name, methods, args)
	if err != nil {
		return err
	}
	_, err = e.applyFunctionWithThis(method, args, handler)
	return err
}

func (e *Evaluator) lookupJSONReader(id int64) (*jsonStreamReader, error) {
	e.jsonMu.Lock()
	reader, ok := e.jsonReaders[id]
	e.jsonMu.Unlock()
	if !ok && e.parent != nil {
		return e.parent.lookupJSONReader(id)
	}
	if !ok {
		return nil, fmt.Errorf("JsonReader is closed")
	}
	return reader, nil
}

func (e *Evaluator) jsonReaderHasNext(reader *jsonStreamReader) (bool, error) {
	if reader.pending != nil {
		return true, nil
	}
	if reader.done {
		return false, nil
	}
	event, err := nextJSONEvent(reader)
	if err != nil {
		return false, err
	}
	if event == nil {
		reader.done = true
		return false, nil
	}
	reader.pending = event
	return true, nil
}

func nextJSONEvent(reader *jsonStreamReader) (runtime.Value, error) {
	tokenValue, err := reader.decoder.Token()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		reader.done = true
		parseErr := native.JSONParseError(err, "")
		return jsonEvent("error", native.ParseErrorValue(parseErr)), nil
	}
	switch tok := tokenValue.(type) {
	case json.Delim:
		switch tok {
		case '{':
			reader.stack = append(reader.stack, jsonContext{kind: 'o', expectKey: true})
			return jsonEvent("startObject", runtime.Null{}), nil
		case '}':
			if len(reader.stack) > 0 {
				reader.stack = reader.stack[:len(reader.stack)-1]
			}
			markJSONValueComplete(reader)
			return jsonEvent("endObject", runtime.Null{}), nil
		case '[':
			reader.stack = append(reader.stack, jsonContext{kind: 'a'})
			return jsonEvent("startArray", runtime.Null{}), nil
		case ']':
			if len(reader.stack) > 0 {
				reader.stack = reader.stack[:len(reader.stack)-1]
			}
			markJSONValueComplete(reader)
			return jsonEvent("endArray", runtime.Null{}), nil
		}
	case string:
		if len(reader.stack) > 0 {
			top := &reader.stack[len(reader.stack)-1]
			if top.kind == 'o' && top.expectKey {
				top.expectKey = false
				return jsonEvent("key", runtime.String{Value: tok}), nil
			}
		}
		markJSONValueComplete(reader)
		return jsonEvent("value", runtime.String{Value: tok}), nil
	default:
		value, err := jsonToValue(tok)
		if err != nil {
			reader.done = true
			parseErr := native.NewParseError(err.Error(), "", -1)
			return jsonEvent("error", native.ParseErrorValue(parseErr)), nil
		}
		markJSONValueComplete(reader)
		return jsonEvent("value", value), nil
	}
	return nil, nil
}

func markJSONValueComplete(reader *jsonStreamReader) {
	if len(reader.stack) == 0 {
		return
	}
	top := &reader.stack[len(reader.stack)-1]
	if top.kind == 'o' && !top.expectKey {
		top.expectKey = true
	}
}

func jsonEvent(kind string, value runtime.Value) runtime.Value {
	return runtime.Dict{Entries: map[string]runtime.DictEntry{
		dictKey(runtime.String{Value: "type"}):  {Key: runtime.String{Value: "type"}, Value: runtime.String{Value: kind}},
		dictKey(runtime.String{Value: "value"}): {Key: runtime.String{Value: "value"}, Value: value},
	}}
}

func jsonToValue(value any) (runtime.Value, error) {
	switch value := value.(type) {
	case nil:
		return runtime.Null{}, nil
	case bool:
		return runtime.Bool{Value: value}, nil
	case string:
		return runtime.String{Value: value}, nil
	case json.Number:
		text := value.String()
		if strings.ContainsAny(text, ".eE") {
			decimal, err := runtime.NewDecimalLiteral(text)
			if err != nil {
				return nil, err
			}
			return decimal, nil
		}
		return runtime.NewIntLiteral(text)
	case []any:
		elements := make([]runtime.Value, 0, len(value))
		for _, item := range value {
			converted, err := jsonToValue(item)
			if err != nil {
				return nil, err
			}
			elements = append(elements, converted)
		}
		return &runtime.List{Elements: elements}, nil
	case map[string]any:
		entries := map[string]runtime.DictEntry{}
		for key, item := range value {
			converted, err := jsonToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: key}
			entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: converted}
		}
		return runtime.Dict{Entries: entries}, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value %T", value)
	}
}

func valueToJSON(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Null:
		return nil, nil
	case runtime.Bool:
		return value.Value, nil
	case runtime.SmallInt:
		return value.Value, nil
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64(), nil
		}
		return value.Value.String(), nil
	case runtime.Decimal:
		return json.Number(value.Value.FloatString(10)), nil
	case runtime.Float:
		return value.Value, nil
	case runtime.String:
		return value.Value, nil
	case runtime.Bytes:
		return base64.StdEncoding.EncodeToString(value.Value), nil
	case *runtime.List:
		items := make([]any, 0, len(value.Elements))
		for _, item := range value.Elements {
			converted, err := valueToJSON(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case runtime.Dict:
		items := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("json.stringify only supports dicts with string keys")
			}
			converted, err := valueToJSON(entry.Value)
			if err != nil {
				return nil, err
			}
			items[key.Value] = converted
		}
		return items, nil
	default:
		return nil, fmt.Errorf("json.stringify does not support %s", value.TypeName())
	}
}

func valueToYAML(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Null:
		return nil, nil
	case runtime.Bool:
		return value.Value, nil
	case runtime.SmallInt:
		return value.Value, nil
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64(), nil
		}
		return value.Value.String(), nil
	case runtime.Decimal:
		return value.Value.FloatString(10), nil
	case runtime.Float:
		return value.Value, nil
	case runtime.String:
		return value.Value, nil
	case runtime.Bytes:
		return base64.StdEncoding.EncodeToString(value.Value), nil
	case *runtime.List:
		items := make([]any, 0, len(value.Elements))
		for _, item := range value.Elements {
			converted, err := valueToYAML(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case runtime.Dict:
		items := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("yaml.stringify only supports dicts with string keys")
			}
			converted, err := valueToYAML(entry.Value)
			if err != nil {
				return nil, err
			}
			items[key.Value] = converted
		}
		return items, nil
	default:
		return nil, fmt.Errorf("yaml.stringify does not support %s", value.TypeName())
	}
}

func tomlParseError(err error, text string) native.ParseError {
	var parseErr tomllib.ParseError
	if errors.As(err, &parseErr) {
		message := parseErr.Message
		if message == "" {
			message = parseErr.Error()
		}
		offset := int64(-1)
		if parseErr.Position.Start >= 0 {
			offset = int64(parseErr.Position.Start + 1)
		}
		result := native.NewParseError(message, text, offset)
		if parseErr.Position.Line > 0 {
			result.Line = int64(parseErr.Position.Line)
		}
		return result
	}
	return native.NewParseError(err.Error(), text, -1)
}

func yamlTextParseError(err error, text string) native.ParseError {
	parseErr := native.NewParseError(err.Error(), text, -1)
	if yamlErr, ok := err.(*yamllib.TypeError); ok && len(yamlErr.Errors) > 0 {
		parseErr.Message = strings.Join(yamlErr.Errors, "; ")
	}
	if line := native.ParseLineNumberFromMessage(parseErr.Message); line > 0 {
		parseErr.Line = line
	}
	return parseErr
}

// The library formats positional errors as "line N: ..."; update if that changes.
func nativeToValue(value any) (runtime.Value, error) {
	switch value := value.(type) {
	case nil:
		return runtime.Null{}, nil
	case bool:
		return runtime.Bool{Value: value}, nil
	case int:
		return runtime.NewInt64(int64(value)), nil
	case int64:
		return runtime.NewInt64(value), nil
	case float64:
		return runtime.Float{Value: value}, nil
	case string:
		return runtime.String{Value: value}, nil
	case []byte:
		return runtime.Bytes{Value: value}, nil
	case time.Time:
		return runtime.String{Value: value.UTC().Format(time.RFC3339Nano)}, nil
	case []any:
		elements := make([]runtime.Value, 0, len(value))
		for _, item := range value {
			converted, err := nativeToValue(item)
			if err != nil {
				return nil, err
			}
			elements = append(elements, converted)
		}
		return &runtime.List{Elements: elements}, nil
	case map[string]any:
		entries := map[string]runtime.DictEntry{}
		for key, item := range value {
			converted, err := nativeToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: key}
			entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: converted}
		}
		return runtime.Dict{Entries: entries}, nil
	case map[any]any:
		entries := map[string]runtime.DictEntry{}
		for key, item := range value {
			keyText, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("only string map keys are supported")
			}
			converted, err := nativeToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: keyText}
			entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: converted}
		}
		return runtime.Dict{Entries: entries}, nil
	case map[string]map[string]any:
		entries := map[string]runtime.DictEntry{}
		for key, item := range value {
			converted, err := nativeToValue(item)
			if err != nil {
				return nil, err
			}
			keyValue := runtime.String{Value: key}
			entries[dictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: converted}
		}
		return runtime.Dict{Entries: entries}, nil
	default:
		return nil, fmt.Errorf("unsupported native value %T", value)
	}
}

func valueToTOML(value runtime.Value) (any, error) {
	switch value := value.(type) {
	case runtime.Null:
		return nil, fmt.Errorf("toml.stringify does not support null")
	case runtime.Bool:
		return value.Value, nil
	case runtime.SmallInt:
		return value.Value, nil
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64(), nil
		}
		return value.Value.String(), nil
	case runtime.Decimal:
		return value.Value.FloatString(10), nil
	case runtime.Float:
		return value.Value, nil
	case runtime.String:
		return value.Value, nil
	case runtime.Bytes:
		return base64.StdEncoding.EncodeToString(value.Value), nil
	case *runtime.List:
		items := make([]any, 0, len(value.Elements))
		for _, item := range value.Elements {
			converted, err := valueToTOML(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case runtime.Dict:
		items := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := entry.Key.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("toml.stringify only supports dicts with string keys")
			}
			converted, err := valueToTOML(entry.Value)
			if err != nil {
				return nil, err
			}
			items[key.Value] = converted
		}
		return items, nil
	default:
		return nil, fmt.Errorf("toml.stringify does not support %s", value.TypeName())
	}
}

func isBuiltinErrorClass(name string) bool {
	switch name {
	case "Error", "RuntimeError", "TypeError", "ValueError", "IOError", "ParseError", "MatchError", "ImmutableError", "PermissionError", "AssertionError", "FatalError":
		return true
	default:
		return false
	}
}

func newErrorValue(class string, args []runtime.Value) (runtime.Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("%s expects zero or one argument", class)
	}
	message := ""
	if len(args) == 1 {
		msg, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("%s message must be string", class)
		}
		message = msg.Value
	}
	return runtime.Error{Class: class, Message: message}, nil
}

func (e *Evaluator) evalMethodCall(receiver runtime.Value, name string, args []runtime.Value) (runtime.Value, error) {
	if class, ok := receiver.(*runtime.Class); ok {
		methods := lookupStaticMethodOverloads(class, name)
		if len(methods) > 0 {
			method, err := selectOverload(class.Name+"."+name, methods, args)
			if err != nil {
				return nil, err
			}
			return e.applyFunction(method, args)
		}
		if method, ok := lookupStaticMethod(class, "__callStatic"); ok {
			return e.applyFunction(method, []runtime.Value{runtime.String{Value: name}, &runtime.List{Elements: args}})
		}
		return nil, fmt.Errorf("unknown static method %s.%s", class.Name, name)
	}
	if instance, ok := receiver.(*runtime.Instance); ok {
		methods := lookupMethodOverloads(instance.Class, name)
		if len(methods) > 0 {
			method, err := selectOverload(instance.Class.Name+"."+name, methods, args)
			if err != nil {
				return nil, err
			}
			return e.applyFunctionWithThis(method, args, instance)
		}
		if method, ok := lookupMethod(instance.Class, "__call"); ok {
			return e.applyFunctionWithThis(method, []runtime.Value{runtime.String{Value: name}, &runtime.List{Elements: args}}, instance)
		}
		return nil, fmt.Errorf("unknown method %s.%s", instance.Class.Name, name)
	}
	if target, ok := primitiveConversionTarget(name); ok {
		if target == "int" {
			if text, ok := receiver.(runtime.String); ok && len(args) >= 1 {
				base, err := native.IntBaseArg(args, "string.toInt")
				if err != nil {
					return nil, err
				}
				return native.StringParseBase(text.Value, base, "string.toInt")
			}
		}
		if target == "decimal" && len(args) >= 1 {
			places, err := native.RoundPlacesArg(args, "toDecimal")
			if err != nil {
				return nil, err
			}
			d, err := castValue(receiver, "decimal")
			if err != nil {
				return nil, err
			}
			return native.DecimalQuantize(d.(runtime.Decimal), places, native.RoundHalfAwayZero), nil
		}
		if len(args) != 0 {
			return nil, fmt.Errorf("%s.%s expects no arguments", receiver.TypeName(), name)
		}
		return castValue(receiver, target)
	}
	switch value := receiver.(type) {
	case runtime.Dict:
		switch name {
		case "copy":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.copy expects no arguments")
			}
			copied := runtime.NewDict()
			for _, k := range value.OrderedKeys() {
				copied.PutEntry(k, value.Entries[k])
			}
			return copied, nil
		case "deepCopy":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.deepCopy expects no arguments")
			}
			return runtime.CloneValue(value), nil
		case "keys":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.keys expects no arguments")
			}
			ordered := value.OrderedKeys()
			keys := make([]runtime.Value, 0, len(ordered))
			for _, k := range ordered {
				keys = append(keys, value.Entries[k].Key)
			}
			return &runtime.List{Elements: keys}, nil
		case "values":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.values expects no arguments")
			}
			ordered := value.OrderedKeys()
			values := make([]runtime.Value, 0, len(ordered))
			for _, k := range ordered {
				values = append(values, value.Entries[k].Value)
			}
			return &runtime.List{Elements: values}, nil
		case "items", "entries":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.%s expects no arguments", name)
			}
			ordered := value.OrderedKeys()
			items := make([]runtime.Value, 0, len(ordered))
			for _, k := range ordered {
				entry := value.Entries[k]
				items = append(items, &runtime.List{Elements: []runtime.Value{entry.Key, entry.Value}})
			}
			return &runtime.List{Elements: items}, nil
		case "get":
			if len(args) != 1 {
				return nil, fmt.Errorf("dict.get expects one argument")
			}
			entry, ok := value.Entries[dictKey(args[0])]
			if !ok {
				return runtime.Null{}, nil
			}
			return entry.Value, nil
		case "set", "insert":
			if len(args) != 2 {
				return nil, fmt.Errorf("dict.%s expects two arguments", name)
			}
			if value.Frozen {
				return nil, thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen dict"}}
			}
			value.PutEntry(dictKey(args[0]), runtime.DictEntry{Key: args[0], Value: args[1]})
			return runtime.Null{}, nil
		case "length":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.length expects no arguments")
			}
			return runtime.SmallInt{Value: int64(len(value.Entries))}, nil
		case "isEmpty":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.isEmpty expects no arguments")
			}
			return runtime.Bool{Value: len(value.Entries) == 0}, nil
		case "hasKey":
			if len(args) != 1 {
				return nil, fmt.Errorf("dict.hasKey expects one argument")
			}
			_, ok := value.Entries[dictKey(args[0])]
			return runtime.Bool{Value: ok}, nil
		case "delete", "remove":
			if len(args) != 1 {
				return nil, fmt.Errorf("dict.%s expects one argument", name)
			}
			if value.Frozen {
				return nil, thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen dict"}}
			}
			value.DelEntry(dictKey(args[0]))
			return runtime.Null{}, nil
		case "clear":
			if len(args) != 0 {
				return nil, fmt.Errorf("dict.clear expects no arguments")
			}
			if value.Frozen {
				return nil, thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen dict"}}
			}
			for k := range value.Entries {
				delete(value.Entries, k)
			}
			if value.Order != nil {
				*value.Order = (*value.Order)[:0]
			}
			return runtime.Null{}, nil
		case "contains":
			if len(args) != 1 {
				return nil, fmt.Errorf("dict.contains expects one argument")
			}
			_, ok := value.Entries[dictKey(args[0])]
			return runtime.Bool{Value: ok}, nil
		case "bfs":
			if len(args) != 1 {
				return nil, fmt.Errorf("collections.bfs expects (graph, start)")
			}
			start := args[0]
			seen := map[string]bool{dictKey(start): true}
			queue := []runtime.Value{start}
			visited := []runtime.Value{}
			for len(queue) > 0 {
				node := queue[0]
				queue = queue[1:]
				visited = append(visited, node)
				if entry, ok := value.Entries[dictKey(node)]; ok {
					if neighbors, ok := entry.Value.(*runtime.List); ok {
						for _, nb := range neighbors.Elements {
							k := dictKey(nb)
							if !seen[k] {
								seen[k] = true
								queue = append(queue, nb)
							}
						}
					}
				}
			}
			return &runtime.List{Elements: visited}, nil
		case "dfs":
			if len(args) != 1 {
				return nil, fmt.Errorf("collections.dfs expects (graph, start)")
			}
			start := args[0]
			seen := map[string]bool{}
			stack := []runtime.Value{start}
			visited := []runtime.Value{}
			for len(stack) > 0 {
				node := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				k := dictKey(node)
				if seen[k] {
					continue
				}
				seen[k] = true
				visited = append(visited, node)
				if entry, ok := value.Entries[dictKey(node)]; ok {
					if neighbors, ok := entry.Value.(*runtime.List); ok {
						for i := len(neighbors.Elements) - 1; i >= 0; i-- {
							nb := neighbors.Elements[i]
							if !seen[dictKey(nb)] {
								stack = append(stack, nb)
							}
						}
					}
				}
			}
			return &runtime.List{Elements: visited}, nil
		case "topologicalSort":
			if len(args) != 0 {
				return nil, fmt.Errorf("collections.topologicalSort expects (graph)")
			}
			allNodes := map[string]runtime.Value{}
			inDegree := map[string]int{}
			for _, entry := range value.Entries {
				k := dictKey(entry.Key)
				allNodes[k] = entry.Key
				if _, ok := inDegree[k]; !ok {
					inDegree[k] = 0
				}
				if neighbors, ok := entry.Value.(*runtime.List); ok {
					for _, nb := range neighbors.Elements {
						nbKey := dictKey(nb)
						if _, exists := allNodes[nbKey]; !exists {
							allNodes[nbKey] = nb
						}
						inDegree[nbKey]++
					}
				}
			}
			// Build sorted initial queue for deterministic output.
			zeroKeys := make([]string, 0)
			for k, deg := range inDegree {
				if deg == 0 {
					zeroKeys = append(zeroKeys, k)
				}
			}
			sort.Strings(zeroKeys)
			queue := make([]runtime.Value, 0, len(zeroKeys))
			for _, k := range zeroKeys {
				queue = append(queue, allNodes[k])
			}
			result := []runtime.Value{}
			for len(queue) > 0 {
				node := queue[0]
				queue = queue[1:]
				result = append(result, node)
				if entry, ok := value.Entries[dictKey(node)]; ok {
					if neighbors, ok := entry.Value.(*runtime.List); ok {
						for _, nb := range neighbors.Elements {
							nbKey := dictKey(nb)
							inDegree[nbKey]--
							if inDegree[nbKey] == 0 {
								queue = append(queue, nb)
							}
						}
					}
				}
			}
			if len(result) != len(allNodes) {
				return nil, fmt.Errorf("collections.topologicalSort: cycle detected")
			}
			return &runtime.List{Elements: result}, nil
		case "shortestPath":
			if len(args) != 2 {
				return nil, fmt.Errorf("collections.shortestPath expects (graph, start, end)")
			}
			start, end := args[0], args[1]
			endKey := dictKey(end)
			parent := map[string]runtime.Value{}
			seen := map[string]bool{dictKey(start): true}
			queue := []runtime.Value{start}
			found := false
			for len(queue) > 0 && !found {
				node := queue[0]
				queue = queue[1:]
				if dictKey(node) == endKey {
					found = true
					break
				}
				if entry, ok := value.Entries[dictKey(node)]; ok {
					if neighbors, ok := entry.Value.(*runtime.List); ok {
						for _, nb := range neighbors.Elements {
							k := dictKey(nb)
							if !seen[k] {
								seen[k] = true
								parent[k] = node
								queue = append(queue, nb)
							}
						}
					}
				}
			}
			if !found {
				return runtime.Null{}, nil
			}
			path := []runtime.Value{end}
			cur := end
			for dictKey(cur) != dictKey(start) {
				p, ok := parent[dictKey(cur)]
				if !ok {
					return runtime.Null{}, nil
				}
				path = append([]runtime.Value{p}, path...)
				cur = p
			}
			return &runtime.List{Elements: path}, nil
		case "merge":
			if len(args) != 1 {
				return nil, fmt.Errorf("dict.merge expects one argument")
			}
			other, ok := args[0].(runtime.Dict)
			if !ok {
				return nil, fmt.Errorf("dict.merge expects a dict argument")
			}
			merged := runtime.Dict{Entries: make(map[string]runtime.DictEntry, len(value.Entries)+len(other.Entries))}
			for k, e := range value.Entries {
				merged.Entries[k] = e
			}
			for k, e := range other.Entries {
				merged.Entries[k] = e
			}
			return merged, nil
		}
	case runtime.Set:
		switch name {
		case "copy":
			if len(args) != 0 {
				return nil, fmt.Errorf("set.copy expects no arguments")
			}
			return runtime.Set{Elements: cloneSetEntries(value.Elements)}, nil
		case "deepCopy":
			if len(args) != 0 {
				return nil, fmt.Errorf("set.deepCopy expects no arguments")
			}
			return runtime.CloneValue(value), nil
		case "length":
			if len(args) != 0 {
				return nil, fmt.Errorf("set.length expects no arguments")
			}
			return runtime.SmallInt{Value: int64(len(value.Elements))}, nil
		case "isEmpty":
			if len(args) != 0 {
				return nil, fmt.Errorf("set.isEmpty expects no arguments")
			}
			return runtime.Bool{Value: len(value.Elements) == 0}, nil
		case "contains":
			if len(args) != 1 {
				return nil, fmt.Errorf("set.contains expects one argument")
			}
			_, ok := value.Elements[dictKey(args[0])]
			return runtime.Bool{Value: ok}, nil
		case "add":
			if len(args) != 1 {
				return nil, fmt.Errorf("set.add expects one argument")
			}
			elements := cloneSetEntries(value.Elements)
			elements[dictKey(args[0])] = runtime.SetEntry{Value: args[0]}
			return runtime.Set{Elements: elements}, nil
		case "remove":
			if len(args) != 1 {
				return nil, fmt.Errorf("set.remove expects one argument")
			}
			elements := cloneSetEntries(value.Elements)
			delete(elements, dictKey(args[0]))
			return runtime.Set{Elements: elements}, nil
		case "toList":
			if len(args) != 0 {
				return nil, fmt.Errorf("set.toList expects no arguments")
			}
			return &runtime.List{Elements: orderedSetValues(value)}, nil
		case "union":
			if len(args) != 1 {
				return nil, fmt.Errorf("set.union expects one argument")
			}
			other, ok := args[0].(runtime.Set)
			if !ok {
				return nil, fmt.Errorf("set.union expects set")
			}
			elements := cloneSetEntries(value.Elements)
			for key, entry := range other.Elements {
				elements[key] = entry
			}
			return runtime.Set{Elements: elements}, nil
		case "intersection":
			if len(args) != 1 {
				return nil, fmt.Errorf("set.intersection expects one argument")
			}
			other, ok := args[0].(runtime.Set)
			if !ok {
				return nil, fmt.Errorf("set.intersection expects set")
			}
			elements := map[string]runtime.SetEntry{}
			for key, entry := range value.Elements {
				if _, exists := other.Elements[key]; exists {
					elements[key] = entry
				}
			}
			return runtime.Set{Elements: elements}, nil
		case "difference":
			if len(args) != 1 {
				return nil, fmt.Errorf("set.difference expects one argument")
			}
			other, ok := args[0].(runtime.Set)
			if !ok {
				return nil, fmt.Errorf("set.difference expects set")
			}
			elements := map[string]runtime.SetEntry{}
			for key, entry := range value.Elements {
				if _, exists := other.Elements[key]; !exists {
					elements[key] = entry
				}
			}
			return runtime.Set{Elements: elements}, nil
		}
	case *runtime.List:
		switch name {
		case "copy":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.copy expects no arguments")
			}
			elems := make([]runtime.Value, len(value.Elements))
			copy(elems, value.Elements)
			return &runtime.List{Elements: elems}, nil
		case "deepCopy":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.deepCopy expects no arguments")
			}
			return runtime.CloneValue(value), nil
		case "length":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.length expects no arguments")
			}
			return runtime.SmallInt{Value: int64(len(value.Elements))}, nil
		case "isEmpty":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.isEmpty expects no arguments")
			}
			return runtime.Bool{Value: len(value.Elements) == 0}, nil
		case "get":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.get expects one argument")
			}
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			return listElement(value, i)
		case "set":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.set expects two arguments")
			}
			if value.Frozen {
				return nil, thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen list"}}
			}
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if i < 0 {
				i = len(value.Elements) + i
			}
			if i < 0 || i >= len(value.Elements) {
				return nil, fmt.Errorf("list index out of range")
			}
			value.Elements[i] = args[1]
			return runtime.Null{}, nil
		case "contains":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.contains expects one argument")
			}
			for _, el := range value.Elements {
				eq, err := e.valuesEqual(el, args[0])
				if err != nil {
					return nil, err
				}
				if eq {
					return runtime.Bool{Value: true}, nil
				}
			}
			return runtime.Bool{Value: false}, nil
		case "indexOf":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.indexOf expects one argument")
			}
			for i, el := range value.Elements {
				eq, err := e.valuesEqual(el, args[0])
				if err != nil {
					return nil, err
				}
				if eq {
					return runtime.NewInt64(int64(i)), nil
				}
			}
			return runtime.NewInt64(-1), nil
		case "slice":
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("list.slice expects (start[, end])")
			}
			n := len(value.Elements)
			start, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("list.slice: %v", err)
			}
			if start < 0 {
				start = n + start
			}
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
			end := n
			if len(args) == 2 {
				end, err = indexInt(args[1])
				if err != nil {
					return nil, fmt.Errorf("list.slice: %v", err)
				}
				if end < 0 {
					end = n + end
				}
				if end < 0 {
					end = 0
				}
				if end > n {
					end = n
				}
			}
			if start >= end {
				return &runtime.List{Elements: nil}, nil
			}
			return &runtime.List{Elements: value.Elements[start:end]}, nil
		case "join":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.join expects one argument (separator)")
			}
			sep, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("list.join separator must be a string")
			}
			parts := make([]string, len(value.Elements))
			for i, el := range value.Elements {
				parts[i] = el.Inspect()
			}
			return runtime.String{Value: strings.Join(parts, sep.Value)}, nil
		case "first":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.first expects no arguments")
			}
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			return value.Elements[0], nil
		case "last":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.last expects no arguments")
			}
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			return value.Elements[len(value.Elements)-1], nil
		case "push":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.push expects one argument")
			}
			newElements := make([]runtime.Value, len(value.Elements)+1)
			copy(newElements, value.Elements)
			newElements[len(value.Elements)] = args[0]
			return &runtime.List{Elements: newElements}, nil
		case "append":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.append expects one argument")
			}
			if value.Frozen {
				return nil, thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen list"}}
			}
			if len(value.ElementTypes) > 0 {
				if !typeNameSatisfies(args[0].TypeName(), value.ElementTypes[0]) {
					return nil, thrownError{value: runtime.Error{
						Class:   "TypeError",
						Message: fmt.Sprintf("cannot append %s to list<%s>", args[0].TypeName(), value.ElementTypes[0]),
					}}
				}
			}
			value.Elements = append(value.Elements, args[0])
			return runtime.Null{}, nil
		case "extend":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.extend expects one argument")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.extend expects a list argument, got %s", args[0].TypeName())
			}
			if value.Frozen {
				return nil, thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen list"}}
			}
			if len(value.ElementTypes) > 0 {
				for i, el := range other.Elements {
					if !typeNameSatisfies(el.TypeName(), value.ElementTypes[0]) {
						return nil, thrownError{value: runtime.Error{
							Class:   "TypeError",
							Message: fmt.Sprintf("cannot extend list<%s> with %s at index %d", value.ElementTypes[0], el.TypeName(), i),
						}}
					}
				}
			}
			value.Elements = append(value.Elements, other.Elements...)
			return runtime.Null{}, nil
		case "clear":
			if value.Frozen {
				return nil, thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen list"}}
			}
			value.Elements = value.Elements[:0]
			return runtime.Null{}, nil
		case "toList":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.toList expects no arguments")
			}
			return value, nil
		case "pop":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.pop expects no arguments")
			}
			if len(value.Elements) == 0 {
				return &runtime.List{Elements: nil}, nil
			}
			newElements := make([]runtime.Value, len(value.Elements)-1)
			copy(newElements, value.Elements[:len(value.Elements)-1])
			return &runtime.List{Elements: newElements}, nil
		case "insert":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.insert expects two arguments (index, item)")
			}
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if i < 0 {
				i = len(value.Elements) + i
			}
			if i < 0 {
				i = 0
			}
			if i > len(value.Elements) {
				i = len(value.Elements)
			}
			newElements := make([]runtime.Value, len(value.Elements)+1)
			copy(newElements, value.Elements[:i])
			newElements[i] = args[1]
			copy(newElements[i+1:], value.Elements[i:])
			return &runtime.List{Elements: newElements}, nil
		case "removeAt":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.removeAt expects one argument")
			}
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if i < 0 {
				i = len(value.Elements) + i
			}
			if i < 0 || i >= len(value.Elements) {
				return nil, fmt.Errorf("list index out of range")
			}
			newElements := make([]runtime.Value, len(value.Elements)-1)
			copy(newElements, value.Elements[:i])
			copy(newElements[i:], value.Elements[i+1:])
			return &runtime.List{Elements: newElements}, nil
		case "concat":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.concat expects one argument")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.concat expects list argument")
			}
			newElements := make([]runtime.Value, len(value.Elements)+len(other.Elements))
			copy(newElements, value.Elements)
			copy(newElements[len(value.Elements):], other.Elements)
			return &runtime.List{Elements: newElements}, nil
		case "map":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.map expects one argument (function)")
			}
			result := make([]runtime.Value, len(value.Elements))
			for i, el := range value.Elements {
				mapped, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				result[i] = mapped
			}
			return &runtime.List{Elements: result}, nil
		case "filter":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.filter expects one argument (function)")
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				keep, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if isTruthy(keep) {
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "reduce":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.reduce expects two arguments (function, initial)")
			}
			acc := args[1]
			for _, el := range value.Elements {
				next, err := e.callValue(args[0], []runtime.Value{acc, el})
				if err != nil {
					return nil, err
				}
				acc = next
			}
			return acc, nil
		case "find":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.find expects one argument (function)")
			}
			for _, el := range value.Elements {
				match, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if isTruthy(match) {
					return el, nil
				}
			}
			return runtime.Null{}, nil
		case "any":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.any expects one argument (function)")
			}
			for _, el := range value.Elements {
				result, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if isTruthy(result) {
					return runtime.Bool{Value: true}, nil
				}
			}
			return runtime.Bool{Value: false}, nil
		case "all":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.all expects one argument (function)")
			}
			for _, el := range value.Elements {
				result, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if !isTruthy(result) {
					return runtime.Bool{Value: false}, nil
				}
			}
			return runtime.Bool{Value: true}, nil
		case "count":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.count expects one argument (function)")
			}
			n := 0
			for _, el := range value.Elements {
				result, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if isTruthy(result) {
					n++
				}
			}
			return runtime.NewInt64(int64(n)), nil
		case "sorted", "sort":
			if len(args) > 1 {
				return nil, fmt.Errorf("list.%s expects zero or one argument", name)
			}
			newElements := make([]runtime.Value, len(value.Elements))
			copy(newElements, value.Elements)
			var sortErr error
			sort.SliceStable(newElements, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				if len(args) == 1 {
					result, err := e.callValue(args[0], []runtime.Value{newElements[i], newElements[j]})
					if err != nil {
						sortErr = err
						return false
					}
					less, err := native.SortLess(result)
					if err != nil {
						sortErr = err
						return false
					}
					return less
				}
				cmp, err := e.compareValues(newElements[i], newElements[j])
				if err != nil {
					sortErr = err
					return false
				}
				return cmp < 0
			})
			if sortErr != nil {
				return nil, sortErr
			}
			return &runtime.List{Elements: newElements}, nil
		case "reverse", "reversed":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.%s expects no arguments", name)
			}
			newElements := make([]runtime.Value, len(value.Elements))
			for i, el := range value.Elements {
				newElements[len(value.Elements)-1-i] = el
			}
			return &runtime.List{Elements: newElements, ElementTypes: value.ElementTypes}, nil
		case "prepend", "unshift":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.%s expects one argument", name)
			}
			newElements := make([]runtime.Value, len(value.Elements)+1)
			newElements[0] = args[0]
			copy(newElements[1:], value.Elements)
			return &runtime.List{Elements: newElements, ElementTypes: value.ElementTypes}, nil
		case "remove":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.remove expects one argument")
			}
			for i, el := range value.Elements {
				eq, err := e.valuesEqual(el, args[0])
				if err != nil {
					return nil, err
				}
				if eq {
					newElements := make([]runtime.Value, len(value.Elements)-1)
					copy(newElements, value.Elements[:i])
					copy(newElements[i:], value.Elements[i+1:])
					return &runtime.List{Elements: newElements, ElementTypes: value.ElementTypes}, nil
				}
			}
			newElements := make([]runtime.Value, len(value.Elements))
			copy(newElements, value.Elements)
			return &runtime.List{Elements: newElements, ElementTypes: value.ElementTypes}, nil
		case "flatten":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.flatten expects no arguments")
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				if nested, ok := el.(*runtime.List); ok {
					result = append(result, nested.Elements...)
				} else {
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "unique":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.unique expects no arguments")
			}
			seen := make([]runtime.Value, 0, len(value.Elements))
			var result []runtime.Value
			for _, el := range value.Elements {
				found := false
				for _, s := range seen {
					eq, err := e.valuesEqual(el, s)
					if err != nil {
						return nil, err
					}
					if eq {
						found = true
						break
					}
				}
				if !found {
					seen = append(seen, el)
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "zip":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.zip expects one argument (list)")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.zip expects list argument")
			}
			n := len(value.Elements)
			if len(other.Elements) < n {
				n = len(other.Elements)
			}
			result := make([]runtime.Value, n)
			for i := 0; i < n; i++ {
				result[i] = &runtime.List{Elements: []runtime.Value{value.Elements[i], other.Elements[i]}}
			}
			return &runtime.List{Elements: result}, nil
		case "groupBy":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.groupBy expects one argument (function)")
			}
			entries := map[string]runtime.DictEntry{}
			for _, el := range value.Elements {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				dk := native.DictKey(key)
				existing, ok := entries[dk]
				if !ok {
					existing = runtime.DictEntry{Key: key, Value: &runtime.List{}}
				}
				existing.Value = &runtime.List{Elements: append(existing.Value.(*runtime.List).Elements, el)}
				entries[dk] = existing
			}
			return runtime.Dict{Entries: entries}, nil
		case "chunk":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.chunk expects one argument (size)")
			}
			n, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if n <= 0 {
				return nil, fmt.Errorf("list.chunk size must be positive")
			}
			var chunks []runtime.Value
			for i := 0; i < len(value.Elements); i += n {
				end := i + n
				if end > len(value.Elements) {
					end = len(value.Elements)
				}
				chunks = append(chunks, &runtime.List{Elements: append([]runtime.Value(nil), value.Elements[i:end]...)})
			}
			return &runtime.List{Elements: chunks}, nil
		case "partition":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.partition expects one argument (function)")
			}
			var yes, no []runtime.Value
			for _, el := range value.Elements {
				result, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if isTruthy(result) {
					yes = append(yes, el)
				} else {
					no = append(no, el)
				}
			}
			return &runtime.List{Elements: []runtime.Value{
				&runtime.List{Elements: yes},
				&runtime.List{Elements: no},
			}}, nil
		case "enumerate":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.enumerate expects no arguments")
			}
			result := make([]runtime.Value, len(value.Elements))
			for i, el := range value.Elements {
				result[i] = &runtime.List{Elements: []runtime.Value{runtime.NewInt64(int64(i)), el}}
			}
			return &runtime.List{Elements: result}, nil
		case "flatMap":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.flatMap expects one argument (function)")
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				mapped, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				nested, ok := mapped.(*runtime.List)
				if !ok {
					return nil, fmt.Errorf("list.flatMap function must return a list")
				}
				result = append(result, nested.Elements...)
			}
			return &runtime.List{Elements: result}, nil
		case "uniqueBy":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.uniqueBy expects one argument (function)")
			}
			seenKeys := make([]runtime.Value, 0, len(value.Elements))
			var result []runtime.Value
			for _, el := range value.Elements {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				found := false
				for _, s := range seenKeys {
					eq, err := e.valuesEqual(key, s)
					if err != nil {
						return nil, err
					}
					if eq {
						found = true
						break
					}
				}
				if !found {
					seenKeys = append(seenKeys, key)
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "takeWhile":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.takeWhile expects one argument (function)")
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				keep, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if !isTruthy(keep) {
					break
				}
				result = append(result, el)
			}
			return &runtime.List{Elements: result}, nil
		case "dropWhile":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.dropWhile expects one argument (function)")
			}
			dropping := true
			var result []runtime.Value
			for _, el := range value.Elements {
				if dropping {
					keep, err := e.callValue(args[0], []runtime.Value{el})
					if err != nil {
						return nil, err
					}
					if isTruthy(keep) {
						continue
					}
					dropping = false
				}
				result = append(result, el)
			}
			return &runtime.List{Elements: result}, nil
		case "windowed":
			if len(args) != 1 && len(args) != 2 {
				return nil, fmt.Errorf("list.windowed expects size and optional step")
			}
			size, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			step := 1
			if len(args) == 2 {
				step, err = indexInt(args[1])
				if err != nil {
					return nil, err
				}
			}
			if size <= 0 || step <= 0 {
				return nil, fmt.Errorf("list.windowed size and step must be positive")
			}
			var windows []runtime.Value
			for i := 0; i+size <= len(value.Elements); i += step {
				windows = append(windows, &runtime.List{Elements: append([]runtime.Value(nil), value.Elements[i:i+size]...)})
			}
			return &runtime.List{Elements: windows}, nil
		case "unzip":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.unzip expects no arguments")
			}
			firsts := make([]runtime.Value, 0, len(value.Elements))
			seconds := make([]runtime.Value, 0, len(value.Elements))
			for _, el := range value.Elements {
				pair, ok := el.(*runtime.List)
				if !ok || len(pair.Elements) != 2 {
					return nil, fmt.Errorf("list.unzip expects a list of 2-element lists")
				}
				firsts = append(firsts, pair.Elements[0])
				seconds = append(seconds, pair.Elements[1])
			}
			return &runtime.List{Elements: []runtime.Value{
				&runtime.List{Elements: firsts},
				&runtime.List{Elements: seconds},
			}}, nil
		case "scan":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.scan expects two arguments (initial, function)")
			}
			acc := args[0]
			result := []runtime.Value{acc}
			for _, el := range value.Elements {
				next, err := e.callValue(args[1], []runtime.Value{acc, el})
				if err != nil {
					return nil, err
				}
				acc = next
				result = append(result, acc)
			}
			return &runtime.List{Elements: result}, nil
		case "findLast":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.findLast expects one argument (function)")
			}
			for i := len(value.Elements) - 1; i >= 0; i-- {
				result, err := e.callValue(args[0], []runtime.Value{value.Elements[i]})
				if err != nil {
					return nil, err
				}
				if isTruthy(result) {
					return value.Elements[i], nil
				}
			}
			return runtime.Null{}, nil
		case "containsBy":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.containsBy expects two arguments (value, function)")
			}
			target, fn := args[0], args[1]
			for _, el := range value.Elements {
				key, err := e.callValue(fn, []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				eq, err := e.valuesEqual(key, target)
				if err != nil {
					return nil, err
				}
				if eq {
					return runtime.Bool{Value: true}, nil
				}
			}
			return runtime.Bool{Value: false}, nil
		case "indexBy":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.indexBy expects one argument (function)")
			}
			for i, el := range value.Elements {
				result, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if isTruthy(result) {
					return runtime.NewInt64(int64(i)), nil
				}
			}
			return runtime.NewInt64(-1), nil
		case "binarySearch":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.binarySearch expects one argument (value)")
			}
			target := args[0]
			lo, hi := 0, len(value.Elements)
			for lo < hi {
				mid := (lo + hi) / 2
				cmp, err := compareValues(value.Elements[mid], target)
				if err != nil {
					return nil, err
				}
				if cmp == 0 {
					return runtime.NewInt64(int64(mid)), nil
				} else if cmp < 0 {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			return runtime.NewInt64(-1), nil
		case "binarySearchBy":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.binarySearchBy expects a selector and a target key")
			}
			target := args[1]
			lo, hi := 0, len(value.Elements)
			for lo < hi {
				mid := (lo + hi) / 2
				key, err := e.callValue(args[0], []runtime.Value{value.Elements[mid]})
				if err != nil {
					return nil, err
				}
				cmp, err := compareValues(key, target)
				if err != nil {
					return nil, err
				}
				if cmp == 0 {
					return runtime.NewInt64(int64(mid)), nil
				} else if cmp < 0 {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			return runtime.NewInt64(-1), nil
		case "lowerBound":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.lowerBound expects one argument (value)")
			}
			target := args[0]
			lo, hi := 0, len(value.Elements)
			for lo < hi {
				mid := (lo + hi) / 2
				cmp, err := compareValues(value.Elements[mid], target)
				if err != nil {
					return nil, err
				}
				if cmp < 0 {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			return runtime.NewInt64(int64(lo)), nil
		case "upperBound":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.upperBound expects one argument (value)")
			}
			target := args[0]
			lo, hi := 0, len(value.Elements)
			for lo < hi {
				mid := (lo + hi) / 2
				cmp, err := compareValues(value.Elements[mid], target)
				if err != nil {
					return nil, err
				}
				if cmp <= 0 {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			return runtime.NewInt64(int64(lo)), nil
		case "minBy":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.minBy expects one argument (function)")
			}
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			best := value.Elements[0]
			bestKey, err := e.callValue(args[0], []runtime.Value{best})
			if err != nil {
				return nil, err
			}
			for _, el := range value.Elements[1:] {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				cmp, err := compareValues(key, bestKey)
				if err != nil {
					return nil, err
				}
				if cmp < 0 {
					best, bestKey = el, key
				}
			}
			return best, nil
		case "maxBy":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.maxBy expects one argument (function)")
			}
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			best := value.Elements[0]
			bestKey, err := e.callValue(args[0], []runtime.Value{best})
			if err != nil {
				return nil, err
			}
			for _, el := range value.Elements[1:] {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				cmp, err := compareValues(key, bestKey)
				if err != nil {
					return nil, err
				}
				if cmp > 0 {
					best, bestKey = el, key
				}
			}
			return best, nil
		case "sortBy":
			if len(args) != 1 && len(args) != 2 {
				return nil, fmt.Errorf("list.sortBy expects a selector and an optional descending flag")
			}
			descending := false
			if len(args) == 2 {
				b, ok := args[1].(runtime.Bool)
				if !ok {
					return nil, fmt.Errorf("list.sortBy descending flag must be a bool")
				}
				descending = b.Value
			}
			type keyedEl struct {
				key runtime.Value
				el  runtime.Value
			}
			pairs := make([]keyedEl, len(value.Elements))
			for i, el := range value.Elements {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				pairs[i] = keyedEl{key, el}
			}
			var sortErr error
			sort.SliceStable(pairs, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				cmp, err := compareValues(pairs[i].key, pairs[j].key)
				if err != nil {
					sortErr = err
					return false
				}
				if descending {
					return cmp > 0
				}
				return cmp < 0
			})
			if sortErr != nil {
				return nil, sortErr
			}
			result := make([]runtime.Value, len(pairs))
			for i, p := range pairs {
				result[i] = p.el
			}
			return &runtime.List{Elements: result}, nil
		case "topBy":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.topBy expects two arguments (function, count)")
			}
			nInt64, ok := toInt64(args[1])
			if !ok {
				return nil, fmt.Errorf("list.topBy: count must be an integer")
			}
			n := int(nInt64)
			type keyedEl struct {
				key runtime.Value
				el  runtime.Value
			}
			pairs := make([]keyedEl, len(value.Elements))
			for i, el := range value.Elements {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				pairs[i] = keyedEl{key, el}
			}
			var sortErr error
			sort.SliceStable(pairs, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				cmp, err := compareValues(pairs[i].key, pairs[j].key)
				if err != nil {
					sortErr = err
					return false
				}
				return cmp > 0
			})
			if sortErr != nil {
				return nil, sortErr
			}
			if n < 0 {
				n = 0
			}
			if n > len(pairs) {
				n = len(pairs)
			}
			result := make([]runtime.Value, n)
			for i := 0; i < n; i++ {
				result[i] = pairs[i].el
			}
			return &runtime.List{Elements: result}, nil
		case "sumBy":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.sumBy expects one argument (function)")
			}
			sum := new(big.Rat)
			hasFloat := false
			var floatSum float64
			for _, el := range value.Elements {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				switch k := key.(type) {
				case runtime.Int:
					if hasFloat {
						f, _ := new(big.Float).SetInt(k.Value).Float64()
						floatSum += f
					} else {
						sum.Add(sum, new(big.Rat).SetInt(k.Value))
					}
				case runtime.Decimal:
					if hasFloat {
						f, _ := k.Value.Float64()
						floatSum += f
					} else {
						sum.Add(sum, k.Value)
					}
				case runtime.Float:
					if !hasFloat {
						floatSum, _ = sum.Float64()
						hasFloat = true
					}
					floatSum += k.Value
				default:
					return nil, fmt.Errorf("list.sumBy: selector must return a number, got %s", key.TypeName())
				}
			}
			if hasFloat {
				return runtime.Float{Value: floatSum}, nil
			}
			if sum.IsInt() {
				return runtime.Int{Value: new(big.Int).Set(sum.Num())}, nil
			}
			return runtime.Decimal{Value: sum}, nil
		case "averageBy":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.averageBy expects one argument (function)")
			}
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			sum := new(big.Rat)
			hasFloat := false
			var floatSum float64
			for _, el := range value.Elements {
				key, err := e.callValue(args[0], []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				switch k := key.(type) {
				case runtime.Int:
					if hasFloat {
						f, _ := new(big.Float).SetInt(k.Value).Float64()
						floatSum += f
					} else {
						sum.Add(sum, new(big.Rat).SetInt(k.Value))
					}
				case runtime.Decimal:
					if hasFloat {
						f, _ := k.Value.Float64()
						floatSum += f
					} else {
						sum.Add(sum, k.Value)
					}
				case runtime.Float:
					if !hasFloat {
						floatSum, _ = sum.Float64()
						hasFloat = true
					}
					floatSum += k.Value
				default:
					return nil, fmt.Errorf("list.averageBy: selector must return a number, got %s", key.TypeName())
				}
			}
			count := int64(len(value.Elements))
			if hasFloat {
				return runtime.Float{Value: floatSum / float64(count)}, nil
			}
			avg := new(big.Rat).Quo(sum, new(big.Rat).SetInt64(count))
			if avg.IsInt() {
				return runtime.Int{Value: new(big.Int).Set(avg.Num())}, nil
			}
			return runtime.Decimal{Value: avg}, nil
		case "topK":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.topK expects one argument (count)")
			}
			nInt64, ok := toInt64(args[0])
			if !ok {
				return nil, fmt.Errorf("list.topK: count must be an integer")
			}
			n := int(nInt64)
			newElements := make([]runtime.Value, len(value.Elements))
			copy(newElements, value.Elements)
			var sortErr error
			sort.SliceStable(newElements, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				cmp, err := compareValues(newElements[i], newElements[j])
				if err != nil {
					sortErr = err
					return false
				}
				return cmp > 0
			})
			if sortErr != nil {
				return nil, sortErr
			}
			if n < 0 {
				n = 0
			}
			if n > len(newElements) {
				n = len(newElements)
			}
			return &runtime.List{Elements: newElements[:n]}, nil
		case "bottomK":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.bottomK expects one argument (count)")
			}
			nInt64, ok := toInt64(args[0])
			if !ok {
				return nil, fmt.Errorf("list.bottomK: count must be an integer")
			}
			n := int(nInt64)
			newElements := make([]runtime.Value, len(value.Elements))
			copy(newElements, value.Elements)
			var sortErr error
			sort.SliceStable(newElements, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				cmp, err := compareValues(newElements[i], newElements[j])
				if err != nil {
					sortErr = err
					return false
				}
				return cmp < 0
			})
			if sortErr != nil {
				return nil, sortErr
			}
			if n < 0 {
				n = 0
			}
			if n > len(newElements) {
				n = len(newElements)
			}
			return &runtime.List{Elements: newElements[:n]}, nil
		case "frequencies":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.frequencies expects no arguments")
			}
			type countEntry struct {
				value runtime.Value
				count int
			}
			seen := map[string]int{}
			var counts []countEntry
			for _, el := range value.Elements {
				k := el.Inspect()
				if idx, ok := seen[k]; ok {
					counts[idx].count++
				} else {
					seen[k] = len(counts)
					counts = append(counts, countEntry{el, 1})
				}
			}
			entries := map[string]runtime.DictEntry{}
			for _, c := range counts {
				entries[native.DictKey(c.value)] = runtime.DictEntry{Key: c.value, Value: runtime.NewInt64(int64(c.count))}
			}
			return runtime.Dict{Entries: entries}, nil
		case "mode":
			if len(args) != 0 {
				return nil, fmt.Errorf("list.mode expects no arguments")
			}
			if len(value.Elements) == 0 {
				return runtime.Null{}, nil
			}
			type countEntry struct {
				value runtime.Value
				count int
			}
			seen := map[string]int{}
			var counts []countEntry
			for _, el := range value.Elements {
				k := el.Inspect()
				if idx, ok := seen[k]; ok {
					counts[idx].count++
				} else {
					seen[k] = len(counts)
					counts = append(counts, countEntry{el, 1})
				}
			}
			best := counts[0]
			for _, c := range counts[1:] {
				if c.count > best.count {
					best = c
				}
			}
			return best.value, nil
		case "difference":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.difference expects one argument (list)")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.difference: second argument must be a list")
			}
			exclude := map[string]bool{}
			for _, el := range other.Elements {
				exclude[el.Inspect()] = true
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				if !exclude[el.Inspect()] {
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "intersection":
			if len(args) != 1 {
				return nil, fmt.Errorf("list.intersection expects one argument (list)")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.intersection: second argument must be a list")
			}
			include := map[string]bool{}
			for _, el := range other.Elements {
				include[el.Inspect()] = true
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				if include[el.Inspect()] {
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "differenceBy":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.differenceBy expects two arguments (list, function)")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.differenceBy: second argument must be a list")
			}
			fn := args[1]
			exclude := map[string]bool{}
			for _, el := range other.Elements {
				key, err := e.callValue(fn, []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				exclude[key.Inspect()] = true
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				key, err := e.callValue(fn, []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if !exclude[key.Inspect()] {
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "intersectionBy":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.intersectionBy expects two arguments (list, function)")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.intersectionBy: second argument must be a list")
			}
			fn := args[1]
			include := map[string]bool{}
			for _, el := range other.Elements {
				key, err := e.callValue(fn, []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				include[key.Inspect()] = true
			}
			var result []runtime.Value
			for _, el := range value.Elements {
				key, err := e.callValue(fn, []runtime.Value{el})
				if err != nil {
					return nil, err
				}
				if include[key.Inspect()] {
					result = append(result, el)
				}
			}
			return &runtime.List{Elements: result}, nil
		case "zipWith":
			if len(args) != 2 {
				return nil, fmt.Errorf("list.zipWith expects two arguments (list, function)")
			}
			other, ok := args[0].(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("list.zipWith: second argument must be a list")
			}
			fn := args[1]
			n := len(value.Elements)
			if len(other.Elements) < n {
				n = len(other.Elements)
			}
			result := make([]runtime.Value, n)
			for i := 0; i < n; i++ {
				combined, err := e.callValue(fn, []runtime.Value{value.Elements[i], other.Elements[i]})
				if err != nil {
					return nil, err
				}
				result[i] = combined
			}
			return &runtime.List{Elements: result}, nil
		}
	case runtime.String:
		switch name {
		case "length":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.length expects no arguments")
			}
			return runtime.SmallInt{Value: int64(len([]rune(value.Value)))}, nil
		case "isEmpty":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.isEmpty expects no arguments")
			}
			return runtime.Bool{Value: value.Value == ""}, nil
		case "get":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.get expects one argument")
			}
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			return stringElement(value, i)
		case "contains":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.contains expects one argument")
			}
			needle, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.contains expects string")
			}
			return runtime.Bool{Value: strings.Contains(value.Value, needle.Value)}, nil
		case "startsWith":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.startsWith expects one argument")
			}
			prefix, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.startsWith expects string")
			}
			return runtime.Bool{Value: strings.HasPrefix(value.Value, prefix.Value)}, nil
		case "endsWith":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.endsWith expects one argument")
			}
			suffix, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.endsWith expects string")
			}
			return runtime.Bool{Value: strings.HasSuffix(value.Value, suffix.Value)}, nil
		case "trim":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.trim expects no arguments")
			}
			return runtime.String{Value: strings.TrimSpace(value.Value)}, nil
		case "trimStart":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.trimStart expects no arguments")
			}
			return runtime.String{Value: strings.TrimLeftFunc(value.Value, unicode.IsSpace)}, nil
		case "trimEnd":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.trimEnd expects no arguments")
			}
			return runtime.String{Value: strings.TrimRightFunc(value.Value, unicode.IsSpace)}, nil
		case "repeat":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.repeat expects one argument")
			}
			n, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("string.repeat: %v", err)
			}
			if n < 0 {
				n = 0
			}
			return runtime.String{Value: strings.Repeat(value.Value, n)}, nil
		case "padStart":
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("string.padStart expects (length[, pad])")
			}
			targetLen, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("string.padStart: %v", err)
			}
			pad := " "
			if len(args) == 2 {
				padStr, ok := args[1].(runtime.String)
				if !ok || len(padStr.Value) == 0 {
					return nil, fmt.Errorf("string.padStart: pad must be a non-empty string")
				}
				pad = padStr.Value
			}
			runes := []rune(value.Value)
			padRunes := []rune(pad)
			for len(runes) < targetLen {
				needed := targetLen - len(runes)
				if needed < len(padRunes) {
					runes = append(padRunes[:needed], runes...)
				} else {
					runes = append(padRunes, runes...)
				}
			}
			if len(runes) > targetLen {
				runes = runes[len(runes)-targetLen:]
			}
			return runtime.String{Value: string(runes)}, nil
		case "padEnd":
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("string.padEnd expects (length[, pad])")
			}
			targetLen, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("string.padEnd: %v", err)
			}
			pad := " "
			if len(args) == 2 {
				padStr, ok := args[1].(runtime.String)
				if !ok || len(padStr.Value) == 0 {
					return nil, fmt.Errorf("string.padEnd: pad must be a non-empty string")
				}
				pad = padStr.Value
			}
			runes := []rune(value.Value)
			padRunes := []rune(pad)
			for len(runes) < targetLen {
				needed := targetLen - len(runes)
				if needed < len(padRunes) {
					runes = append(runes, padRunes[:needed]...)
				} else {
					runes = append(runes, padRunes...)
				}
			}
			if len(runes) > targetLen {
				runes = runes[:targetLen]
			}
			return runtime.String{Value: string(runes)}, nil
		case "chars":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.chars expects no arguments")
			}
			runes := []rune(value.Value)
			elements := make([]runtime.Value, len(runes))
			for i, r := range runes {
				elements[i] = runtime.String{Value: string(r)}
			}
			return &runtime.List{Elements: elements}, nil
		case "codePoints":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.codePoints expects no arguments")
			}
			runes := []rune(value.Value)
			elements := make([]runtime.Value, len(runes))
			for i, r := range runes {
				elements[i] = runtime.NewInt64(int64(r))
			}
			return &runtime.List{Elements: elements}, nil
		case "graphemes":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.graphemes expects no arguments")
			}
			clusters := native.Graphemes(value.Value)
			elements := make([]runtime.Value, len(clusters))
			for i, c := range clusters {
				elements[i] = runtime.String{Value: c}
			}
			return &runtime.List{Elements: elements}, nil
		case "graphemeLength":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.graphemeLength expects no arguments")
			}
			return runtime.NewInt64(int64(native.GraphemeCount(value.Value))), nil
		case "truncateGraphemes":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.truncateGraphemes expects one argument")
			}
			n, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("string.truncateGraphemes: %v", err)
			}
			return runtime.String{Value: native.TruncateGraphemes(value.Value, n)}, nil
		case "codePointAt":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.codePointAt expects one argument")
			}
			idx, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("string.codePointAt: %v", err)
			}
			runes := []rune(value.Value)
			if idx < 0 {
				idx = len(runes) + idx
			}
			if idx < 0 || idx >= len(runes) {
				return runtime.Null{}, nil
			}
			return runtime.NewInt64(int64(runes[idx])), nil
		case "format":
			formatted, err := formatString(value.Value, args)
			if err != nil {
				return nil, err
			}
			return runtime.String{Value: formatted}, nil
		case "lower":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.lower expects no arguments")
			}
			return runtime.String{Value: strings.ToLower(value.Value)}, nil
		case "upper":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.upper expects no arguments")
			}
			return runtime.String{Value: strings.ToUpper(value.Value)}, nil
		case "capitalize":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.capitalize expects no arguments")
			}
			return runtime.String{Value: native.StringCapitalize(value.Value)}, nil
		case "title":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.title expects no arguments")
			}
			return runtime.String{Value: native.StringTitle(value.Value)}, nil
		case "isBlank":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.isBlank expects no arguments")
			}
			return runtime.Bool{Value: native.StringIsBlank(value.Value)}, nil
		case "lines":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.lines expects no arguments")
			}
			parts := native.StringLines(value.Value)
			out := make([]runtime.Value, 0, len(parts))
			for _, part := range parts {
				out = append(out, runtime.String{Value: part})
			}
			return &runtime.List{Elements: out}, nil
		case "removePrefix", "removeSuffix":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.%s expects one argument (string)", name)
			}
			affix, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.%s expects string", name)
			}
			if name == "removePrefix" {
				return runtime.String{Value: native.StringRemovePrefix(value.Value, affix.Value)}, nil
			}
			return runtime.String{Value: native.StringRemoveSuffix(value.Value, affix.Value)}, nil
		case "equalsIgnoreCase":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.equalsIgnoreCase expects one argument (string)")
			}
			other, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.equalsIgnoreCase expects string")
			}
			return runtime.Bool{Value: native.StringEqualsIgnoreCase(value.Value, other.Value)}, nil
		case "containsIgnoreCase":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.containsIgnoreCase expects one argument (string)")
			}
			sub, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.containsIgnoreCase expects string")
			}
			return runtime.Bool{Value: native.StringContainsIgnoreCase(value.Value, sub.Value)}, nil
		case "replace":
			if len(args) != 2 && len(args) != 3 {
				return nil, fmt.Errorf("string.replace expects old, new, and optional count")
			}
			oldValue, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.replace old value must be string")
			}
			newValue, ok := args[1].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.replace new value must be string")
			}
			count := -1
			if len(args) == 3 {
				var err error
				count, err = indexInt(args[2])
				if err != nil {
					return nil, err
				}
			}
			return runtime.String{Value: strings.Replace(value.Value, oldValue.Value, newValue.Value, count)}, nil
		case "split":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.split expects one argument")
			}
			sep, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.split expects string")
			}
			parts := strings.Split(value.Value, sep.Value)
			out := make([]runtime.Value, 0, len(parts))
			for _, part := range parts {
				out = append(out, runtime.String{Value: part})
			}
			return &runtime.List{Elements: out}, nil
		case "splitRegex":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.splitRegex expects one argument")
			}
			return e.natives.Call("re", "split", []runtime.Value{args[0], value})
		case "replaceRegex":
			if len(args) != 2 {
				return nil, fmt.Errorf("string.replaceRegex expects (pattern, replacement)")
			}
			return e.natives.Call("re", "replace", []runtime.Value{args[0], args[1], value})
		case "matchesRegex":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.matchesRegex expects one argument")
			}
			return e.natives.Call("re", "test", []runtime.Value{args[0], value})
		case "indexOf":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.indexOf expects one argument")
			}
			needle, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.indexOf expects string")
			}
			byteIndex := strings.Index(value.Value, needle.Value)
			if byteIndex < 0 {
				return runtime.NewInt64(-1), nil
			}
			return runtime.SmallInt{Value: int64(len([]rune(value.Value[:byteIndex])))}, nil
		case "substring", "slice":
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("string.%s expects (start[, end])", name)
			}
			runes := []rune(value.Value)
			n := len(runes)
			start, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("string.%s: %v", name, err)
			}
			if start < 0 {
				start = n + start
			}
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
			end := n
			if len(args) == 2 {
				end, err = indexInt(args[1])
				if err != nil {
					return nil, fmt.Errorf("string.%s: %v", name, err)
				}
				if end < 0 {
					end = n + end
				}
				if end < 0 {
					end = 0
				}
				if end > n {
					end = n
				}
			}
			if start >= end {
				return runtime.String{Value: ""}, nil
			}
			return runtime.String{Value: string(runes[start:end])}, nil
		case "toString":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.toString expects no arguments")
			}
			return value, nil
		case "lastIndexOf":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.lastIndexOf expects one argument")
			}
			needle, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.lastIndexOf expects string")
			}
			byteIndex := strings.LastIndex(value.Value, needle.Value)
			if byteIndex < 0 {
				return runtime.NewInt64(-1), nil
			}
			return runtime.SmallInt{Value: int64(len([]rune(value.Value[:byteIndex])))}, nil
		case "reverse":
			if len(args) != 0 {
				return nil, fmt.Errorf("string.reverse expects no arguments")
			}
			runes := []rune(value.Value)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return runtime.String{Value: string(runes)}, nil
		case "count":
			if len(args) != 1 {
				return nil, fmt.Errorf("string.count expects one argument")
			}
			needle, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("string.count expects string")
			}
			return runtime.NewInt64(int64(strings.Count(value.Value, needle.Value))), nil
		}
	case runtime.Bytes:
		switch name {
		case "length":
			if len(args) != 0 {
				return nil, fmt.Errorf("bytes.length expects no arguments")
			}
			return runtime.SmallInt{Value: int64(len(value.Value))}, nil
		case "isEmpty":
			if len(args) != 0 {
				return nil, fmt.Errorf("bytes.isEmpty expects no arguments")
			}
			return runtime.Bool{Value: len(value.Value) == 0}, nil
		case "get":
			if len(args) != 1 {
				return nil, fmt.Errorf("bytes.get expects one argument")
			}
			i, err := indexInt(args[0])
			if err != nil {
				return nil, err
			}
			if i < 0 {
				i = len(value.Value) + i
			}
			if i < 0 || i >= len(value.Value) {
				return nil, fmt.Errorf("bytes index out of range")
			}
			return runtime.NewInt64(int64(value.Value[i])), nil
		case "toString":
			data, err := bytesWithOptionalUTF8Encoding(&ast.CallExpression{Callee: &ast.Identifier{Value: "bytes.toString"}}, append([]runtime.Value{value}, args...))
			if err != nil {
				return nil, err
			}
			return runtime.String{Value: string(data)}, nil
		case "toHex":
			if len(args) != 0 {
				return nil, fmt.Errorf("bytes.toHex expects no arguments")
			}
			return runtime.String{Value: hex.EncodeToString(value.Value)}, nil
		case "toBase64":
			if len(args) != 0 {
				return nil, fmt.Errorf("bytes.toBase64 expects no arguments")
			}
			return runtime.String{Value: base64.StdEncoding.EncodeToString(value.Value)}, nil
		case "toBase64Url":
			if len(args) != 0 {
				return nil, fmt.Errorf("bytes.toBase64Url expects no arguments")
			}
			return runtime.String{Value: base64.RawURLEncoding.EncodeToString(value.Value)}, nil
		case "toList":
			if len(args) != 0 {
				return nil, fmt.Errorf("bytes.toList expects no arguments")
			}
			elements := make([]runtime.Value, len(value.Value))
			for i, b := range value.Value {
				elements[i] = runtime.NewInt64(int64(b))
			}
			return &runtime.List{Elements: elements}, nil
		case "contains":
			if len(args) != 1 {
				return nil, fmt.Errorf("bytes.contains expects one argument")
			}
			if needle, ok := args[0].(runtime.Bytes); ok {
				return runtime.Bool{Value: bytes.Contains(value.Value, needle.Value)}, nil
			}
			needleVal, ok := toInt64(args[0])
			if !ok {
				return nil, fmt.Errorf("bytes.contains expects bytes or int byte")
			}
			b := needleVal
			if b < 0 || b > 255 {
				return runtime.Bool{Value: false}, nil
			}
			return runtime.Bool{Value: bytes.Contains(value.Value, []byte{byte(b)})}, nil
		case "slice":
			if len(args) < 1 || len(args) > 2 {
				return nil, fmt.Errorf("bytes.slice expects (start[, end])")
			}
			n := len(value.Value)
			start, err := indexInt(args[0])
			if err != nil {
				return nil, fmt.Errorf("bytes.slice: %v", err)
			}
			if start < 0 {
				start = n + start
			}
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
			end := n
			if len(args) == 2 {
				end, err = indexInt(args[1])
				if err != nil {
					return nil, fmt.Errorf("bytes.slice: %v", err)
				}
				if end < 0 {
					end = n + end
				}
				if end < start {
					end = start
				}
				if end > n {
					end = n
				}
			}
			out := make([]byte, end-start)
			copy(out, value.Value[start:end])
			return runtime.Bytes{Value: out}, nil
		}
	case runtime.Bool:
		switch name {
		case "toString":
			if len(args) != 0 {
				return nil, fmt.Errorf("bool.toString expects no arguments")
			}
			return runtime.String{Value: value.Inspect()}, nil
		case "not":
			if len(args) != 0 {
				return nil, fmt.Errorf("bool.not expects no arguments")
			}
			return runtime.Bool{Value: !value.Value}, nil
		}
	case runtime.SmallInt:
		// Promote and re-dispatch through the Int branch so every int
		// method works on both runtime representations.
		return e.evalMethodCall(runtime.Int{Value: big.NewInt(value.Value)}, name, args)
	case runtime.Int:
		switch name {
		case "abs":
			if len(args) != 0 {
				return nil, fmt.Errorf("int.abs expects no arguments")
			}
			return native.NumericAbs(value)
		case "isZero":
			if len(args) != 0 {
				return nil, fmt.Errorf("int.isZero expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Sign() == 0}, nil
		case "isPositive":
			if len(args) != 0 {
				return nil, fmt.Errorf("int.isPositive expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Sign() > 0}, nil
		case "isNegative":
			if len(args) != 0 {
				return nil, fmt.Errorf("int.isNegative expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Sign() < 0}, nil
		case "toString":
			base, err := native.IntBaseArg(args, "int.toString")
			if err != nil {
				return nil, err
			}
			if base == 10 {
				return runtime.String{Value: value.Inspect()}, nil
			}
			s, err := native.IntFormatBase(value, base)
			if err != nil {
				return nil, err
			}
			return runtime.String{Value: s}, nil
		case "sign":
			if len(args) != 0 {
				return nil, fmt.Errorf("int.sign expects no arguments")
			}
			return native.NumericSign(value)
		case "clamp":
			if len(args) != 2 {
				return nil, fmt.Errorf("int.clamp expects two arguments")
			}
			return native.NumericClamp(value, args[0], args[1])
		case "isEven":
			if len(args) != 0 {
				return nil, fmt.Errorf("int.isEven expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Bit(0) == 0}, nil
		case "isOdd":
			if len(args) != 0 {
				return nil, fmt.Errorf("int.isOdd expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Bit(0) == 1}, nil
		}
	case runtime.Decimal:
		switch name {
		case "abs":
			if len(args) != 0 {
				return nil, fmt.Errorf("decimal.abs expects no arguments")
			}
			return native.NumericAbs(value)
		case "isZero":
			if len(args) != 0 {
				return nil, fmt.Errorf("decimal.isZero expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Sign() == 0}, nil
		case "isPositive":
			if len(args) != 0 {
				return nil, fmt.Errorf("decimal.isPositive expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Sign() > 0}, nil
		case "isNegative":
			if len(args) != 0 {
				return nil, fmt.Errorf("decimal.isNegative expects no arguments")
			}
			return runtime.Bool{Value: value.Value.Sign() < 0}, nil
		case "toString":
			if len(args) > 1 {
				return nil, fmt.Errorf("decimal.toString expects optional scale")
			}
			scale := 10
			if len(args) == 1 {
				var err error
				scale, err = decimalFormatScale(args[0])
				if err != nil {
					return nil, err
				}
			}
			return runtime.String{Value: value.Value.FloatString(scale)}, nil
		case "format":
			if len(args) != 1 {
				return nil, fmt.Errorf("decimal.format expects scale")
			}
			scale, err := decimalFormatScale(args[0])
			if err != nil {
				return nil, err
			}
			return runtime.String{Value: value.Value.FloatString(scale)}, nil
		case "round":
			return native.NumericRoundMethod(value, args, native.RoundHalfAwayZero, "decimal.round")
		case "floor":
			return native.NumericRoundMethod(value, args, native.RoundFloor, "decimal.floor")
		case "ceil":
			return native.NumericRoundMethod(value, args, native.RoundCeil, "decimal.ceil")
		case "truncate":
			return native.NumericRoundMethod(value, args, native.RoundTrunc, "decimal.truncate")
		case "sign":
			if len(args) != 0 {
				return nil, fmt.Errorf("decimal.sign expects no arguments")
			}
			return native.NumericSign(value)
		case "clamp":
			if len(args) != 2 {
				return nil, fmt.Errorf("decimal.clamp expects two arguments")
			}
			return native.NumericClamp(value, args[0], args[1])
		}
	case runtime.Range:
		switch name {
		case "length":
			if len(args) != 0 {
				return nil, fmt.Errorf("range.length expects no arguments")
			}
			return runtime.Int{Value: value.Length()}, nil
		case "isEmpty":
			if len(args) != 0 {
				return nil, fmt.Errorf("range.isEmpty expects no arguments")
			}
			return runtime.Bool{Value: value.Length().Sign() == 0}, nil
		case "contains":
			if len(args) != 1 {
				return nil, fmt.Errorf("range.contains expects one argument")
			}
			n, ok := args[0].(runtime.Int)
			if !ok {
				return runtime.Bool{Value: false}, nil
			}
			return runtime.Bool{Value: value.ContainsInt(n.Value)}, nil
		case "first":
			if len(args) != 0 {
				return nil, fmt.Errorf("range.first expects no arguments")
			}
			if value.Length().Sign() == 0 {
				return runtime.Null{}, nil
			}
			return runtime.Int{Value: new(big.Int).Set(value.Start)}, nil
		case "last":
			if len(args) != 0 {
				return nil, fmt.Errorf("range.last expects no arguments")
			}
			n := value.Length()
			if n.Sign() == 0 {
				return runtime.Null{}, nil
			}
			last := new(big.Int).Mul(value.Step, new(big.Int).Sub(n, big.NewInt(1)))
			last.Add(last, value.Start)
			return runtime.Int{Value: last}, nil
		case "toList":
			if len(args) != 0 {
				return nil, fmt.Errorf("range.toList expects no arguments")
			}
			var elements []runtime.Value
			current := new(big.Int).Set(value.Start)
			step := value.Step
			for {
				cmp := current.Cmp(value.End)
				if step.Sign() > 0 {
					if value.Exclusive && cmp >= 0 {
						break
					}
					if !value.Exclusive && cmp > 0 {
						break
					}
				} else {
					if value.Exclusive && cmp <= 0 {
						break
					}
					if !value.Exclusive && cmp < 0 {
						break
					}
				}
				elements = append(elements, runtime.Int{Value: new(big.Int).Set(current)})
				current.Add(current, step)
			}
			return &runtime.List{Elements: elements}, nil
		}
	case runtime.Float:
		switch name {
		case "abs":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.abs expects no arguments")
			}
			return native.NumericAbs(value)
		case "isZero":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.isZero expects no arguments")
			}
			return runtime.Bool{Value: value.Value == 0}, nil
		case "isPositive":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.isPositive expects no arguments")
			}
			return runtime.Bool{Value: value.Value > 0}, nil
		case "isNegative":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.isNegative expects no arguments")
			}
			return runtime.Bool{Value: value.Value < 0}, nil
		case "isNaN":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.isNaN expects no arguments")
			}
			return runtime.Bool{Value: math.IsNaN(value.Value)}, nil
		case "isInf":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.isInf expects no arguments")
			}
			return runtime.Bool{Value: math.IsInf(value.Value, 0)}, nil
		case "toString":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.toString expects no arguments")
			}
			return runtime.String{Value: value.Inspect()}, nil
		case "round":
			return native.NumericRoundMethod(value, args, native.RoundHalfAwayZero, "float.round")
		case "floor":
			return native.NumericRoundMethod(value, args, native.RoundFloor, "float.floor")
		case "ceil":
			return native.NumericRoundMethod(value, args, native.RoundCeil, "float.ceil")
		case "truncate":
			return native.NumericRoundMethod(value, args, native.RoundTrunc, "float.truncate")
		case "sign":
			if len(args) != 0 {
				return nil, fmt.Errorf("float.sign expects no arguments")
			}
			return native.NumericSign(value)
		case "clamp":
			if len(args) != 2 {
				return nil, fmt.Errorf("float.clamp expects two arguments")
			}
			return native.NumericClamp(value, args[0], args[1])
		}
	}
	return nil, fmt.Errorf("unknown method %s.%s", receiver.TypeName(), name)
}

func formatArgs(args []runtime.Value) []any {
	out := make([]any, len(args))
	for i, arg := range args {
		out[i] = formatArg(arg)
	}
	return out
}

func formatString(format string, args []runtime.Value) (string, error) {
	formatted := fmt.Sprintf(format, formatArgs(args)...)
	if strings.Contains(formatted, "%!") {
		return "", fmt.Errorf("invalid string.format arguments for %q", format)
	}
	return formatted, nil
}

func decimalFormatScale(value runtime.Value) (int, error) {
	scale, err := indexInt(value)
	if err != nil {
		return 0, fmt.Errorf("decimal scale must be int")
	}
	if scale < 0 || scale > 10000 {
		return 0, fmt.Errorf("decimal scale must be between 0 and 10000")
	}
	return scale, nil
}

func formatArg(value runtime.Value) any {
	switch value := value.(type) {
	case runtime.Null:
		return nil
	case runtime.Bool:
		return value.Value
	case runtime.Int:
		if value.Value.IsInt64() {
			return value.Value.Int64()
		}
		return value.Value.String()
	case runtime.Decimal:
		f, _ := value.Value.Float64()
		return f
	case runtime.Float:
		return value.Value
	case runtime.String:
		return value.Value
	case runtime.Bytes:
		return value.Value
	default:
		return value.Inspect()
	}
}

func (e *Evaluator) evalMethodCallExpression(receiver runtime.Value, name string, call *ast.CallExpression, env *runtime.Environment) (runtime.Value, error) {
	if enumDef, ok := receiver.(*runtime.EnumDef); ok {
		args, err := e.evalCallArguments(call, env)
		if err != nil {
			return nil, err
		}
		for _, v := range enumDef.Variants {
			if strings.EqualFold(v.Name, name) {
				if v.FieldCount != len(args) {
					return nil, fmt.Errorf("enum variant %s.%s expects %d argument(s), got %d", enumDef.Name, v.Name, v.FieldCount, len(args))
				}
				return runtime.EnumVariant{Enum: enumDef, Variant: v.Name, Fields: args}, nil
			}
		}
		return nil, fmt.Errorf("enum %s has no variant %s", enumDef.Name, name)
	}
	if class, ok := receiver.(*runtime.Class); ok {
		methods := lookupStaticMethodOverloads(class, name)
		if len(methods) > 0 {
			return e.applyOverloadedFunction(class.Name+"."+name, methods, call, env, nil, nil)
		}
		args, err := e.evalCallArguments(call, env)
		if err != nil {
			return nil, err
		}
		if method, ok := lookupStaticMethod(class, "__callStatic"); ok {
			return e.applyFunction(method, []runtime.Value{runtime.String{Value: name}, &runtime.List{Elements: args}})
		}
		return nil, fmt.Errorf("unknown static method %s.%s", class.Name, name)
	}
	if instance, ok := receiver.(*runtime.Instance); ok {
		methods := lookupMethodOverloads(instance.Class, name)
		if len(methods) > 0 {
			return e.applyOverloadedFunction(instance.Class.Name+"."+name, methods, call, env, instance, nil)
		}
		args, err := e.evalCallArguments(call, env)
		if err != nil {
			return nil, err
		}
		if method, ok := lookupMethod(instance.Class, "__call"); ok {
			return e.applyFunctionWithThis(method, []runtime.Value{runtime.String{Value: name}, &runtime.List{Elements: args}}, instance)
		}
		return nil, fmt.Errorf("unknown method %s.%s", instance.Class.Name, name)
	}
	args, err := e.evalCallArguments(call, env)
	if err != nil {
		return nil, err
	}
	if nativeObject, ok := receiver.(runtime.NativeObject); ok {
		switch nativeObject.Kind {
		case "IOBuffer":
			return e.ioBufferMethod(nativeObject, name, args)
		case "IOStream", "IOCapture":
			return e.ioStreamMethod(nativeObject, name, args)
		case "JsonReader":
			return e.jsonReaderMethod(nativeObject, name, args)
		case "XmlReader":
			return e.xmlReaderMethod(nativeObject, name, args)
		case "CsvReader":
			return e.csvReaderMethod(nativeObject, name, args)
		case "YamlReader":
			return e.yamlReaderMethod(nativeObject, name, args)
		default:
			return nil, fmt.Errorf("%s has no method %s", nativeObject.TypeName(), name)
		}
	}
	if instant, ok := receiver.(runtime.DateTimeInstant); ok {
		return native.DateTimeInstantMethod(instant, name, args)
	}
	if duration, ok := receiver.(runtime.DateTimeDuration); ok {
		return native.DateTimeDurationMethod(duration, name, args)
	}
	if zone, ok := receiver.(runtime.DateTimeZone); ok {
		return native.DateTimeZoneMethod(zone, name, args)
	}
	if urlValue, ok := receiver.(runtime.URLValue); ok {
		return native.URLMethod(urlValue, name, args)
	}
	if headers, ok := receiver.(runtime.HTTPHeaders); ok {
		return httpHeadersMethod(headers, name, args)
	}
	if cookie, ok := receiver.(runtime.HTTPCookie); ok {
		return native.HTTPCookieMethod(cookie, name, args)
	}
	if tmpl, ok := receiver.(runtime.TemplateValue); ok {
		return native.TemplateMethod(tmpl, name, args)
	}
	if engine, ok := receiver.(runtime.TemplateEngine); ok {
		return native.TemplateEngineMethod(engine, name, args)
	}
	if errValue, ok := receiver.(runtime.Error); ok {
		return native.ErrorMethod(errValue, name, args)
	}
	if trace, ok := receiver.(runtime.ErrorStackTrace); ok {
		return native.ErrorStackTraceMethod(trace, name, args)
	}
	if frame, ok := receiver.(runtime.ErrorStackFrame); ok {
		return native.ErrorStackFrameMethod(frame, name, args)
	}
	if task, ok := receiver.(*runtime.Task); ok {
		switch name {
		case "await":
			if len(args) != 0 {
				return nil, fmt.Errorf("Task.await expects no arguments")
			}
			return awaitValue(task)
		case "done":
			if len(args) != 0 {
				return nil, fmt.Errorf("Task.done expects no arguments")
			}
			return runtime.Bool{Value: task.Done()}, nil
		case "cancel":
			if len(args) != 0 {
				return nil, fmt.Errorf("Task.cancel expects no arguments")
			}
			task.Cancel()
			return runtime.Null{}, nil
		case "cancelled":
			if len(args) != 0 {
				return nil, fmt.Errorf("Task.cancelled expects no arguments")
			}
			return runtime.Bool{Value: task.Cancelled()}, nil
		default:
			return nil, fmt.Errorf("Task has no method %s", name)
		}
	}
	// Static methods on a builtin type value (bytes.fromString, string.fromCodePoint)
	// resolve without an import, matching the bytecode VM.
	if typeVal, ok := receiver.(runtime.Type); ok {
		if functions, ok := e.builtins[typeVal.Name]; ok {
			if fn, ok := functions[name]; ok {
				return fn(call, args)
			}
		}
	}
	return e.evalMethodCall(receiver, name, args)
}

func (e *Evaluator) evalParentMethodCall(selector *ast.SelectorExpression, call *ast.CallExpression, env *runtime.Environment) (runtime.Value, bool, error) {
	object, ok := selector.Object.(*ast.Identifier)
	if !ok || object.Value != "parent" {
		return nil, false, nil
	}
	this, err := currentInstance(env)
	if err != nil {
		return nil, true, err
	}
	if this.Class.Parent == nil {
		return nil, true, fmt.Errorf("%s has no parent class", this.Class.Name)
	}
	methods := lookupMethodOverloads(this.Class.Parent, selector.Name.Value)
	if len(methods) == 0 {
		return nil, true, fmt.Errorf("unknown parent method %s.%s", this.Class.Parent.Name, selector.Name.Value)
	}
	value, err := e.applyOverloadedFunction(this.Class.Parent.Name+"."+selector.Name.Value, methods, call, env, this, nil)
	return value, true, err
}

func (e *Evaluator) evalIndexExpression(expr *ast.IndexExpression, env *runtime.Environment) (runtime.Value, error) {
	left, err := e.evalExpression(expr.Left, env)
	if err != nil {
		return nil, err
	}
	if rng, ok := expr.Index.(*ast.RangeExpression); ok {
		return e.evalSliceExpression(left, rng, env)
	}
	index, err := e.evalExpression(expr.Index, env)
	if err != nil {
		return nil, err
	}
	switch value := left.(type) {
	case *runtime.List:
		i, err := indexInt(index)
		if err != nil {
			return nil, err
		}
		return listElement(value, i)
	case runtime.Dict:
		entry, ok := value.Entries[dictKey(index)]
		if !ok {
			return runtime.Null{}, nil
		}
		return entry.Value, nil
	case runtime.String:
		i, err := indexInt(index)
		if err != nil {
			return nil, err
		}
		return stringElement(value, i)
	case runtime.Bytes:
		i, err := indexInt(index)
		if err != nil {
			return nil, err
		}
		return bytesElement(value, i)
	default:
		return nil, fmt.Errorf("%s is not indexable", left.TypeName())
	}
}

func (e *Evaluator) evalSliceExpression(left runtime.Value, rng *ast.RangeExpression, env *runtime.Environment) (runtime.Value, error) {
	switch value := left.(type) {
	case *runtime.List:
		indices, err := e.sliceIndices(rng, len(value.Elements), env)
		if err != nil {
			return nil, err
		}
		elements := make([]runtime.Value, len(indices))
		for i, idx := range indices {
			elements[i] = value.Elements[idx]
		}
		return &runtime.List{Elements: elements}, nil
	case runtime.String:
		runes := []rune(value.Value)
		indices, err := e.sliceIndices(rng, len(runes), env)
		if err != nil {
			return nil, err
		}
		out := make([]rune, len(indices))
		for i, idx := range indices {
			out[i] = runes[idx]
		}
		return runtime.String{Value: string(out)}, nil
	case runtime.Bytes:
		indices, err := e.sliceIndices(rng, len(value.Value), env)
		if err != nil {
			return nil, err
		}
		out := make([]byte, len(indices))
		for i, idx := range indices {
			out[i] = value.Value[idx]
		}
		return runtime.Bytes{Value: out}, nil
	default:
		return nil, fmt.Errorf("%s does not support slicing", left.TypeName())
	}
}

// sliceIndices computes the indices a slice expression produces, honouring
// Python-style step (including negative). When step is absent (Step == nil),
// it falls back to sliceBounds for the contiguous case.
func (e *Evaluator) sliceIndices(rng *ast.RangeExpression, length int, env *runtime.Environment) ([]int, error) {
	step := 1
	if rng.Step != nil {
		v, err := e.evalExpression(rng.Step, env)
		if err != nil {
			return nil, err
		}
		s, err := indexInt(v)
		if err != nil {
			return nil, err
		}
		step = s
	}
	if step == 0 {
		return nil, fmt.Errorf("slice step cannot be zero")
	}
	if step == 1 {
		start, end, err := e.sliceBounds(rng, length, env)
		if err != nil {
			return nil, err
		}
		out := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			out = append(out, i)
		}
		return out, nil
	}
	// Python-style start/stop defaults depend on step sign.
	var start, stop int
	if step > 0 {
		start = 0
		stop = length
	} else {
		start = length - 1
		stop = -1
	}
	if rng.Start != nil {
		v, err := e.evalExpression(rng.Start, env)
		if err != nil {
			return nil, err
		}
		i, err := indexInt(v)
		if err != nil {
			return nil, err
		}
		if i < 0 {
			i += length
		}
		if step > 0 {
			if i < 0 {
				i = 0
			}
			if i > length {
				i = length
			}
		} else {
			if i < -1 {
				i = -1
			}
			if i > length-1 {
				i = length - 1
			}
		}
		start = i
	}
	if rng.End != nil {
		v, err := e.evalExpression(rng.End, env)
		if err != nil {
			return nil, err
		}
		i, err := indexInt(v)
		if err != nil {
			return nil, err
		}
		if !rng.Exclusive {
			if step > 0 {
				i++
			} else {
				i--
			}
		}
		if i < 0 {
			i += length
		}
		if step > 0 {
			if i < 0 {
				i = 0
			}
			if i > length {
				i = length
			}
		} else {
			if i < -1 {
				i = -1
			}
			if i > length-1 {
				i = length - 1
			}
		}
		stop = i
	}
	out := []int{}
	if step > 0 {
		for i := start; i < stop; i += step {
			out = append(out, i)
		}
	} else {
		for i := start; i > stop; i += step {
			out = append(out, i)
		}
	}
	return out, nil
}

func (e *Evaluator) sliceBounds(rng *ast.RangeExpression, length int, env *runtime.Environment) (int, int, error) {
	start := 0
	end := length
	if rng.Start != nil {
		value, err := e.evalExpression(rng.Start, env)
		if err != nil {
			return 0, 0, err
		}
		start, err = indexInt(value)
		if err != nil {
			return 0, 0, err
		}
	}
	if rng.End != nil {
		value, err := e.evalExpression(rng.End, env)
		if err != nil {
			return 0, 0, err
		}
		end, err = indexInt(value)
		if err != nil {
			return 0, 0, err
		}
		if !rng.Exclusive {
			end++
		}
	}
	if start < 0 {
		start = length + start
	}
	if end < 0 {
		end = length + end
	}
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start > length {
		start = length
	}
	if end > length {
		end = length
	}
	if end < start {
		end = start
	}
	return start, end, nil
}

func (e *Evaluator) assignIndex(expr *ast.IndexExpression, newValue runtime.Value, env *runtime.Environment) error {
	left, err := e.evalExpression(expr.Left, env)
	if err != nil {
		return err
	}
	index, err := e.evalExpression(expr.Index, env)
	if err != nil {
		return err
	}
	switch value := left.(type) {
	case *runtime.List:
		if value.Frozen {
			return thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen list"}}
		}
		i, err := indexInt(index)
		if err != nil {
			return err
		}
		if i < 0 {
			i = len(value.Elements) + i
		}
		if i < 0 || i >= len(value.Elements) {
			return fmt.Errorf("list index out of range")
		}
		value.Elements[i] = newValue
		return nil
	case runtime.Dict:
		if value.Frozen {
			return thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen dict"}}
		}
		value.PutEntry(dictKey(index), runtime.DictEntry{Key: index, Value: newValue})
		return nil
	default:
		return fmt.Errorf("%s does not support index assignment", left.TypeName())
	}
}

func (e *Evaluator) assignSelector(expr *ast.SelectorExpression, newValue runtime.Value, env *runtime.Environment) error {
	object, err := e.evalExpression(expr.Object, env)
	if err != nil {
		return err
	}
	instance, ok := object.(*runtime.Instance)
	if ok {
		if instance.Frozen {
			return thrownError{value: runtime.Error{Class: "ImmutableError", Message: "cannot modify frozen instance of " + instance.Class.Name}}
		}
		transformed, err := e.applyFieldDecorators(instance.Class, expr.Name.Value, newValue, env)
		if err != nil {
			return err
		}
		newValue = transformed
		if instance.HasField(expr.Name.Value) {
			instance.SetField(expr.Name.Value, newValue)
			return nil
		}
		if method, ok := lookupMethod(instance.Class, "__set"); ok {
			_, err := e.applyFunctionWithThis(method, []runtime.Value{runtime.String{Value: expr.Name.Value}, newValue}, instance)
			return err
		}
		return fmt.Errorf("unknown field %s.%s", instance.Class.Name, expr.Name.Value)
	}
	class, ok := object.(*runtime.Class)
	if ok {
		// Direct assignment to a static let / const member (immutable
		// const is enforced at the declaration site by the analyzer;
		// the runtime treats all StaticValues as writable so user
		// code that genuinely intends mutation through `static let`
		// works).
		for cur := class; cur != nil; cur = cur.Parent {
			if _, present := cur.StaticValues[expr.Name.Value]; present {
				cur.StaticValues[expr.Name.Value] = newValue
				return nil
			}
		}
		if method, ok := lookupStaticMethod(class, "__setStatic"); ok {
			_, err := e.applyFunction(method, []runtime.Value{runtime.String{Value: expr.Name.Value}, newValue})
			return err
		}
		return fmt.Errorf("unknown static member %s.%s", class.Name, expr.Name.Value)
	}
	return fmt.Errorf("%s does not support field assignment", object.TypeName())
}

// evalRangeBuiltin implements the top-level `range(start, end[, step])`
// shorthand, producing a list<int> inclusive of both endpoints. Negative
// steps are allowed when start > end. Step zero is rejected.
func (e *Evaluator) evalAssertCall(call *ast.CallExpression, env *runtime.Environment) (runtime.Value, error) {
	if e.AssertionsDisabled {
		return runtime.Null{}, nil
	}
	if len(call.Arguments) < 1 || len(call.Arguments) > 2 {
		return nil, fmt.Errorf("assert expects (condition) or (condition, message)")
	}
	for _, arg := range call.Arguments {
		if arg.Name != nil {
			return nil, fmt.Errorf("assert does not accept named arguments")
		}
	}
	condValue, err := e.evalExpression(call.Arguments[0].Value, env)
	if err != nil {
		return nil, err
	}
	if isTruthy(condValue) {
		return runtime.Null{}, nil
	}
	message := "assertion failed: " + call.Arguments[0].Value.String()
	if len(call.Arguments) == 2 {
		msgValue, err := e.evalExpression(call.Arguments[1].Value, env)
		if err != nil {
			return nil, err
		}
		msgStr, ok := msgValue.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("assert message must be string")
		}
		message = msgStr.Value
	}
	return nil, thrownError{value: e.withTrace(runtime.Error{Class: "AssertionError", Message: message, Parents: e.errorParentChain("AssertionError")})}
}

func (e *Evaluator) evalRangeBuiltin(call *ast.CallExpression, env *runtime.Environment) (runtime.Value, error) {
	args, err := e.evalCallArguments(call, env)
	if err != nil {
		return nil, err
	}
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("range expects (start, end) or (start, end, step)")
	}
	startBig, ok := native.IntValueToBigInt(args[0])
	if !ok {
		return nil, fmt.Errorf("range start must be int")
	}
	endBig, ok := native.IntValueToBigInt(args[1])
	if !ok {
		return nil, fmt.Errorf("range end must be int")
	}
	step := big.NewInt(1)
	if len(args) == 3 {
		stepBig, ok := native.IntValueToBigInt(args[2])
		if !ok {
			return nil, fmt.Errorf("range step must be int")
		}
		step = stepBig
	} else if startBig.Cmp(endBig) > 0 {
		step = big.NewInt(-1)
	}
	if step.Sign() == 0 {
		return nil, fmt.Errorf("range step cannot be zero")
	}
	rng := runtime.Range{Start: new(big.Int).Set(startBig), End: new(big.Int).Set(endBig), Exclusive: false, Step: new(big.Int).Set(step)}
	return rangeToList(rng), nil
}

// tagCollectionWithTypeRef returns a copy of the given collection
// value with its ElementTypes field populated from the declared
// TypeRef. Non-collection values are returned unchanged. The tag
// covers list<T>, T[], set<T>, dict<K,V>. Untagged collections that
// flow through this path gain a fresh tag; already-tagged ones are
// rebound to the new annotation.
func tagCollectionWithTypeRef(value runtime.Value, typ *ast.TypeRef) runtime.Value {
	if typ == nil || typ.Operator != "" {
		return value
	}
	switch v := value.(type) {
	case *runtime.List:
		var tag []string
		if typ.ListAlias && len(typ.Arguments) == 0 && typ.Name != "" && !strings.EqualFold(typ.Name, "list") {
			tag = []string{typ.Name}
		} else if len(typ.Arguments) > 0 {
			tag = make([]string, 0, len(typ.Arguments))
			for _, arg := range typ.Arguments {
				if arg == nil {
					tag = append(tag, "")
					continue
				}
				tag = append(tag, arg.Name)
			}
		}
		if tag != nil {
			v.ElementTypes = tag
		}
		return v
	case runtime.Set:
		if len(typ.Arguments) > 0 && typ.Arguments[0] != nil {
			v.ElementTypes = []string{typ.Arguments[0].Name}
		}
		return v
	case runtime.Dict:
		if len(typ.Arguments) >= 2 && typ.Arguments[0] != nil && typ.Arguments[1] != nil {
			v.ElementTypes = []string{typ.Arguments[0].Name, typ.Arguments[1].Name}
		}
		return v
	}
	return value
}

// rangeToList materialises a range value as a list<int>. Shared by
// `range()` and `Range.toList()` so both produce identical output.
func rangeToList(rng runtime.Range) *runtime.List {
	var elements []runtime.Value
	current := new(big.Int).Set(rng.Start)
	step := rng.Step
	for {
		cmp := current.Cmp(rng.End)
		if step.Sign() > 0 {
			if rng.Exclusive && cmp >= 0 {
				break
			}
			if !rng.Exclusive && cmp > 0 {
				break
			}
		} else {
			if rng.Exclusive && cmp <= 0 {
				break
			}
			if !rng.Exclusive && cmp < 0 {
				break
			}
		}
		elements = append(elements, runtime.Int{Value: new(big.Int).Set(current)})
		current.Add(current, step)
	}
	return &runtime.List{Elements: elements}
}

func (e *Evaluator) evalRangeExpression(expr *ast.RangeExpression, env *runtime.Environment) (runtime.Value, error) {
	start := big.NewInt(0)
	var startStr *runtime.String
	if expr.Start != nil {
		value, err := e.evalExpression(expr.Start, env)
		if err != nil {
			return nil, err
		}
		if s, ok := value.(runtime.String); ok && len([]rune(s.Value)) == 1 {
			startStr = &s
		} else {
			startBig, ok := native.IntValueToBigInt(value)
			if !ok {
				return nil, fmt.Errorf("range start must be int")
			}
			start = startBig
		}
	}
	if expr.End == nil {
		return nil, fmt.Errorf("open-ended ranges are not evaluated outside slices yet")
	}
	endValue, err := e.evalExpression(expr.End, env)
	if err != nil {
		return nil, err
	}
	if startStr != nil {
		// Char range: 'a'..'z' produces an eager list<string> of single
		// character entries covering the inclusive codepoint span. The
		// `..<` exclusive form omits the last element. Step is fixed at +1.
		endStr, ok := endValue.(runtime.String)
		if !ok || len([]rune(endStr.Value)) != 1 {
			return nil, fmt.Errorf("char range end must be a single-character string")
		}
		startRune := []rune(startStr.Value)[0]
		endRune := []rune(endStr.Value)[0]
		var elements []runtime.Value
		if startRune <= endRune {
			for r := startRune; r <= endRune; r++ {
				if expr.Exclusive && r == endRune {
					break
				}
				elements = append(elements, runtime.String{Value: string(r)})
			}
		} else {
			for r := startRune; r >= endRune; r-- {
				if expr.Exclusive && r == endRune {
					break
				}
				elements = append(elements, runtime.String{Value: string(r)})
			}
		}
		return &runtime.List{Elements: elements}, nil
	}
	endBig, ok := native.IntValueToBigInt(endValue)
	if !ok {
		return nil, fmt.Errorf("range end must be int")
	}
	step := big.NewInt(1)
	if expr.Step != nil {
		stepValue, err := e.evalExpression(expr.Step, env)
		if err != nil {
			return nil, err
		}
		stepBig, ok := native.IntValueToBigInt(stepValue)
		if !ok {
			return nil, fmt.Errorf("range step must be int")
		}
		step = stepBig
	}
	if step.Sign() == 0 {
		return nil, fmt.Errorf("range step cannot be zero")
	}
	return runtime.Range{Start: new(big.Int).Set(start), End: new(big.Int).Set(endBig), Exclusive: expr.Exclusive, Step: new(big.Int).Set(step)}, nil
}

func (e *Evaluator) evalCallArguments(call *ast.CallExpression, env *runtime.Environment) ([]runtime.Value, error) {
	args := make([]runtime.Value, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		if arg.Name != nil {
			return nil, fmt.Errorf("named arguments are only supported for Geblang functions and methods")
		}
		value, err := e.evalExpression(arg.Value, env)
		if err != nil {
			return nil, err
		}
		if arg.Spread {
			list, ok := value.(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("spread argument must be a list")
			}
			args = append(args, list.Elements...)
			continue
		}
		args = append(args, value)
	}
	return args, nil
}

type evaluatedCallArg struct {
	name  string
	value runtime.Value
	// fromSpread: binder silently drops unknown names; explicit named args don't.
	fromSpread bool
}

func (e *Evaluator) evalDetailedCallArguments(call *ast.CallExpression, env *runtime.Environment) ([]evaluatedCallArg, error) {
	args := make([]evaluatedCallArg, 0, len(call.Arguments))
	for _, arg := range call.Arguments {
		value, err := e.evalExpression(arg.Value, env)
		if err != nil {
			return nil, err
		}
		if arg.Spread {
			switch value := value.(type) {
			case *runtime.List:
				for _, elem := range value.Elements {
					args = append(args, evaluatedCallArg{name: "", value: elem})
				}
			case runtime.Dict:
				expanded, err := dictSpreadCallArguments(value)
				if err != nil {
					return nil, err
				}
				args = append(args, expanded...)
			default:
				return nil, fmt.Errorf("spread argument must be a list or dict")
			}
			continue
		}
		name := ""
		if arg.Name != nil {
			name = arg.Name.Value
		}
		args = append(args, evaluatedCallArg{name: name, value: value})
	}
	return args, nil
}

func dictSpreadCallArguments(dict runtime.Dict) ([]evaluatedCallArg, error) {
	type namedArg struct {
		name  string
		value runtime.Value
	}
	named := make([]namedArg, 0, len(dict.Entries))
	for _, entry := range dict.Entries {
		key, ok := entry.Key.(runtime.String)
		if !ok {
			return nil, fmt.Errorf("spread dict argument keys must be strings")
		}
		named = append(named, namedArg{name: key.Value, value: entry.Value})
	}
	sort.Slice(named, func(i, j int) bool { return named[i].name < named[j].name })
	args := make([]evaluatedCallArg, 0, len(named))
	for _, arg := range named {
		args = append(args, evaluatedCallArg{name: arg.name, value: arg.value, fromSpread: true})
	}
	return args, nil
}

func (e *Evaluator) applyOverloadedFunction(label string, overloads []runtime.Function, call *ast.CallExpression, env *runtime.Environment, this *runtime.Instance, expected *ast.TypeRef) (runtime.Value, error) {
	// Build per-argument expected-type hints from overloads that match the outer
	// expected return type. If all candidate overloads agree on a parameter type
	// for a given position, propagate it when evaluating that argument so that
	// nested overloaded calls (e.g. use(make())) can resolve unambiguously.
	hints := overloadParamTypeHints(overloads, len(call.Arguments), expected)
	provided, err := e.evalDetailedCallArgumentsWithHints(call, env, hints)
	if err != nil {
		return nil, err
	}
	var matches []runtime.Function
	var matchedArgs [][]runtime.Value
	var matchedDropped []int
	for _, fn := range overloads {
		args, dropped, ok := bindEvaluatedFunctionCallArgumentsDetail(fn, provided)
		if !ok || !functionArgumentsMatch(fn, args) || !functionReturnMatchesExpected(fn, expected) {
			continue
		}
		matches = append(matches, fn)
		matchedArgs = append(matchedArgs, args)
		matchedDropped = append(matchedDropped, dropped)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no matching overload for %s", label)
	}
	if len(matches) > 1 {
		minDropped := matchedDropped[0]
		for _, d := range matchedDropped[1:] {
			if d < minDropped {
				minDropped = d
			}
		}
		kept := matches[:0]
		keptArgs := matchedArgs[:0]
		for i, fn := range matches {
			if matchedDropped[i] == minDropped {
				kept = append(kept, fn)
				keptArgs = append(keptArgs, matchedArgs[i])
			}
		}
		matches = kept
		matchedArgs = keptArgs
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous overload for %s", label)
	}
	if this != nil && this.Class != nil && strings.EqualFold(label, this.Class.Name) {
		// Skip when dispatching a parent's same-named constructor via parent().
		if matches[0].OwnerClass == nil || matches[0].OwnerClass == this.Class {
			if err := e.applyAutoParentConstructor(this, matches[0]); err != nil {
				return nil, err
			}
		}
	}
	chosen := matches[0]
	if len(call.TypeArguments) > 0 && len(chosen.TypeParameters) > 0 {
		merged := map[string]string{}
		for k, v := range chosen.TypeBindings {
			merged[k] = v
		}
		for i, t := range call.TypeArguments {
			if i >= len(chosen.TypeParameters) {
				break
			}
			if t == nil || t.Operator != "" || t.Name == "" {
				continue
			}
			merged[chosen.TypeParameters[i]] = t.Name
		}
		chosen.TypeBindings = merged
	}
	return e.applyFunctionWithThis(chosen, matchedArgs[0], this)
}

// overloadParamTypeHints returns a per-position expected TypeRef when every
// overload that could match the outer expected return type agrees on the
// parameter type at that position.  A nil entry means no consensus.
func overloadParamTypeHints(overloads []runtime.Function, argc int, expected *ast.TypeRef) []*ast.TypeRef {
	if argc == 0 {
		return nil
	}
	hints := make([]*ast.TypeRef, argc)
	for i := range hints {
		var agreed *ast.TypeRef
		first := true
		for _, fn := range overloads {
			if !functionReturnMatchesExpected(fn, expected) {
				continue
			}
			if i >= len(fn.Parameters) {
				agreed = nil
				first = false
				continue
			}
			param := fn.Parameters[i]
			if param.Type == nil || param.Type.Name == "any" || param.Type.Operator != "" {
				agreed = nil
				first = false
				continue
			}
			if first {
				agreed = param.Type
				first = false
			} else if agreed == nil || param.Type.Name != agreed.Name {
				agreed = nil
				break
			}
		}
		hints[i] = agreed
	}
	return hints
}

func (e *Evaluator) evalDetailedCallArgumentsWithHints(call *ast.CallExpression, env *runtime.Environment, hints []*ast.TypeRef) ([]evaluatedCallArg, error) {
	args := make([]evaluatedCallArg, 0, len(call.Arguments))
	for i, arg := range call.Arguments {
		var hint *ast.TypeRef
		if hints != nil && i < len(hints) {
			hint = hints[i]
		}
		var value runtime.Value
		var err error
		if hint != nil {
			value, err = e.evalExpressionWithExpectedType(arg.Value, env, hint)
		} else {
			value, err = e.evalExpression(arg.Value, env)
		}
		if err != nil {
			return nil, err
		}
		if arg.Spread {
			switch value := value.(type) {
			case *runtime.List:
				for _, elem := range value.Elements {
					args = append(args, evaluatedCallArg{name: "", value: elem})
				}
			case runtime.Dict:
				expanded, err := dictSpreadCallArguments(value)
				if err != nil {
					return nil, err
				}
				args = append(args, expanded...)
			default:
				return nil, fmt.Errorf("spread argument must be a list or dict")
			}
			continue
		}
		name := ""
		if arg.Name != nil {
			name = arg.Name.Value
		}
		args = append(args, evaluatedCallArg{name: name, value: value})
	}
	return args, nil
}

func selectOverload(label string, overloads []runtime.Function, args []runtime.Value) (runtime.Function, error) {
	var matches []runtime.Function
	for _, fn := range overloads {
		if !functionAcceptsPositionalArgs(fn, len(args)) {
			continue
		}
		bound := make([]runtime.Value, len(fn.Parameters))
		copy(bound, args)
		if !functionArgumentsMatch(fn, bound) {
			continue
		}
		matches = append(matches, fn)
	}
	if len(matches) == 0 {
		return runtime.Function{}, fmt.Errorf("no matching overload for %s", label)
	}
	if len(matches) > 1 {
		return runtime.Function{}, fmt.Errorf("ambiguous overload for %s", label)
	}
	return matches[0], nil
}

func functionAcceptsPositionalArgs(fn runtime.Function, count int) bool {
	if fn.Native != nil && len(fn.Parameters) == 0 {
		return true
	}
	if count > len(fn.Parameters) {
		return false
	}
	for i := count; i < len(fn.Parameters); i++ {
		if fn.Parameters[i].Default == nil {
			return false
		}
	}
	return true
}

func bindEvaluatedFunctionCallArguments(fn runtime.Function, provided []evaluatedCallArg) ([]runtime.Value, bool) {
	args, _, ok := bindEvaluatedFunctionCallArgumentsDetail(fn, provided)
	return args, ok
}

// bindEvaluatedFunctionCallArgumentsDetail also reports how many fromSpread
// args were silently dropped. Overload resolution prefers the overload
// that drops fewest so spread + overload disambiguates predictably.
func bindEvaluatedFunctionCallArgumentsDetail(fn runtime.Function, provided []evaluatedCallArg) ([]runtime.Value, int, bool) {
	if fn.Native != nil && len(fn.Parameters) == 0 {
		args := make([]runtime.Value, 0, len(provided))
		for _, arg := range provided {
			if arg.name != "" {
				return nil, 0, false
			}
			args = append(args, arg.value)
		}
		return args, 0, true
	}
	paramNames := map[string]bool{}
	for _, p := range fn.Parameters {
		if p.Name != nil {
			paramNames[p.Name.Value] = true
		}
	}
	dropped := 0
	filtered := make([]evaluatedCallArg, 0, len(provided))
	for _, arg := range provided {
		if arg.fromSpread && arg.name != "" && !paramNames[arg.name] {
			dropped++
			continue
		}
		filtered = append(filtered, arg)
	}
	provided = filtered
	isVariadic := len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Variadic
	if !isVariadic && len(provided) > len(fn.Parameters) {
		return nil, 0, false
	}
	argsLen := len(fn.Parameters)
	if isVariadic && len(provided) > argsLen {
		argsLen = len(provided)
	}
	args := make([]runtime.Value, argsLen)
	filled := make([]bool, len(fn.Parameters))
	positional := 0
	seenNamed := false
	for _, arg := range provided {
		index := -1
		if arg.name == "" {
			if seenNamed || (!isVariadic && positional >= len(fn.Parameters)) {
				return nil, 0, false
			}
			index = positional
			positional++
		} else {
			seenNamed = true
			for i, param := range fn.Parameters {
				if param.Name != nil && param.Name.Value == arg.name {
					index = i
					break
				}
			}
			if index == -1 {
				return nil, 0, false
			}
		}
		if index < len(filled) && filled[index] {
			return nil, 0, false
		}
		args[index] = arg.value
		if index < len(filled) {
			filled[index] = true
		}
	}
	for i, param := range fn.Parameters {
		if param.Variadic {
			continue
		}
		if !filled[i] && param.Default == nil {
			return nil, 0, false
		}
	}
	return args, dropped, true
}

func functionArgumentsMatch(fn runtime.Function, args []runtime.Value) bool {
	if fn.Native != nil && len(fn.Parameters) == 0 {
		return true
	}
	typeParams := functionTypeParameterSetOrNil(fn)
	isVariadic := len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Variadic
	for i, arg := range args {
		if arg == nil {
			continue
		}
		paramIdx := i
		if isVariadic && i >= len(fn.Parameters) {
			paramIdx = len(fn.Parameters) - 1
		} else if i >= len(fn.Parameters) {
			return false
		}
		if !matchValueToTypeRef(typeParams, arg, fn.Parameters[paramIdx].Type) {
			return false
		}
	}
	return true
}

// functionArgumentsMatchError returns nil if all args match, or a descriptive error for the
// first mismatched argument. Used for user-facing type mismatch errors.
func functionArgumentsMatchError(fn runtime.Function, args []runtime.Value) error {
	if fn.Native != nil && len(fn.Parameters) == 0 {
		return nil
	}
	typeParams := functionTypeParameterSetOrNil(fn)
	inherited := fn.TypeBindings
	isVariadic := len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Variadic
	for i, arg := range args {
		if arg == nil {
			continue
		}
		paramIdx := i
		if isVariadic && i >= len(fn.Parameters) {
			paramIdx = len(fn.Parameters) - 1
		} else if i >= len(fn.Parameters) {
			return fmt.Errorf("too many arguments for %s", fn.Name)
		}
		param := fn.Parameters[paramIdx]
		if !matchValueToTypeRefWith(typeParams, inherited, arg, param.Type) {
			paramTypeName := ""
			if param.Type != nil {
				paramTypeName = param.Type.String()
			}
			name := fn.Name
			if name == "" {
				name = "anonymous"
			}
			suffix := collectionMismatchSuffix(arg, param.Type)
			gotName := descriptiveTypeName(arg)
			if suffix != "" {
				gotName = arg.TypeName()
			}
			return fmt.Errorf("%s expects %s for parameter '%s', got %s%s", name, paramTypeName, param.Name.Value, gotName, suffix)
		}
	}
	return nil
}

// snapshotEnvTypeBindings walks the environment chain and copies the
// accumulated type-parameter bindings into a fresh map. Used when a
// FunctionLiteral or identifier-as-callable creates a function value
// whose enclosing scope has active generic bindings.
func snapshotEnvTypeBindings(env *runtime.Environment) map[string]string {
	if env == nil {
		return nil
	}
	out := map[string]string{}
	for _, name := range env.TypeBindingNames() {
		if v, ok := env.GetTypeBinding(name); ok {
			out[name] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// matchValueToTypeRefWith is matchValueToTypeRef extended with an
// inheritedBindings map. When a type-parameter name in the spec is not
// declared by the function being entered but IS bound by the caller's
// outer generic frame, the binding's concrete type is substituted and
// re-checked.
func matchValueToTypeRefWith(typeParams map[string]bool, inherited map[string]string, value runtime.Value, typ *ast.TypeRef) bool {
	if typ == nil {
		return true
	}
	if len(inherited) > 0 && typ.Operator == "" && len(typ.Arguments) == 0 {
		if bound, ok := inherited[typ.Name]; ok && bound != "" {
			// Substitute the concrete type and re-check via the
			// existing path.
			substituted := &ast.TypeRef{Token: typ.Token, Name: bound, Nullable: typ.Nullable}
			return matchValueToTypeRef(typeParams, value, substituted)
		}
	}
	return matchValueToTypeRef(typeParams, value, typ)
}

func functionReturnMatchesExpected(fn runtime.Function, expected *ast.TypeRef) bool {
	if expected == nil || expected.Operator != "" || expected.Name == "any" {
		return true
	}
	if fn.ReturnType == nil {
		return expected.Name == "void" || expected.Nullable
	}
	if typeRefUsesFunctionTypeParameter(fn, fn.ReturnType) {
		return true
	}
	return typeRefAssignable(expected, fn.ReturnType)
}

func typeRefAssignable(target, actual *ast.TypeRef) bool {
	if target == nil || target.Operator != "" || target.Name == "any" {
		return true
	}
	if actual == nil || actual.Operator != "" {
		return false
	}
	if !target.Nullable && actual.Nullable {
		return false
	}
	targetName := target.Name
	actualName := actual.Name
	if target.ListAlias {
		targetName = "list"
	}
	if actual.ListAlias {
		actualName = "list"
	}
	if isCallableTypeName(targetName) && isCallableTypeName(actualName) {
		return true
	}
	if !typeNamesEqual(targetName, actualName) {
		return false
	}
	if len(target.Arguments) > 0 && len(target.Arguments) == len(actual.Arguments) {
		for i, tArg := range target.Arguments {
			if !typeRefAssignable(tArg, actual.Arguments[i]) {
				return false
			}
		}
	}
	return true
}

func valueMatchesTypeRef(value runtime.Value, typ *ast.TypeRef) bool {
	if typ == nil || typ.Operator != "" || typ.Name == "any" {
		return true
	}
	if _, ok := value.(runtime.Null); ok {
		return typ.Nullable
	}
	typeName := simpleTypeName(typ.Name)
	if typ.ListAlias || typeName == "list" {
		return value.TypeName() == "list"
	}
	if typeName == "set" {
		return value.TypeName() == "set"
	}
	if typeName == "dict" {
		return value.TypeName() == "dict"
	}
	if typeName == "Task" {
		if _, ok := value.(*runtime.Task); ok {
			return true
		}
		/* Not the runtime async Task. Fall through so a user-defined
		 * class named `Task` still matches its own instance. The
		 * built-in runtime Task is reachable only through `async`-
		 * returning callsites, so masking a user class with this
		 * name would otherwise reject every dispatch. */
	}
	if isCallableTypeName(typeName) {
		return runtime.IsCallableValue(value)
	}
	if isGeneratorTypeName(typeName) {
		_, ok := value.(*runtime.Generator)
		return ok
	}
	if typeNamesEqual(value.TypeName(), typ.Name) {
		if instance, ok := value.(*runtime.Instance); ok && !instanceMatchesTypeArgs(instance, typ) {
			return false
		}
		return true
	}
	// Error-derived classes are wrapped as runtime.Error rather than
	// *runtime.Instance. Walk the captured parent chain so a parameter
	// typed `HttpException` accepts a `BadRequestError` value.
	if errValue, ok := value.(runtime.Error); ok {
		target := simpleTypeName(typ.Name)
		for _, ancestor := range errValue.Parents {
			if typeNamesEqual(ancestor, target) {
				return true
			}
		}
		return false
	}
	instance, ok := value.(*runtime.Instance)
	if !ok {
		return false
	}
	for class := instance.Class.Parent; class != nil; class = class.Parent {
		if typeNamesEqual(class.Name, typ.Name) {
			if !instanceMatchesTypeArgs(instance, typ) {
				return false
			}
			return true
		}
	}
	return classImplementsInterface(instance.Class, simpleTypeName(typ.Name))
}

// instanceMatchesTypeArgs enforces invariance on the type arguments of a
// reified generic class instance. When the parameter type carries explicit
// arguments (e.g. `Box<Base>`) the instance's `TypeBindings` must match
// each argument exactly - a `Box<Sub>` is NOT a `Box<Base>` even though
// `Sub extends Base`, because mutating methods on the parameter could
// otherwise insert a sibling `Base` subtype that violates the original
// container's declared element type.
//
// When the parameter type carries no arguments, or the instance has no
// recorded bindings (raw polymorphic construction), the check passes -
// invariance only fires when both sides explicitly carry type arguments.
func instanceMatchesTypeArgs(instance *runtime.Instance, typ *ast.TypeRef) bool {
	if instance == nil || typ == nil || len(typ.Arguments) == 0 {
		return true
	}
	if instance.Class == nil || len(instance.Class.TypeParameters) == 0 {
		return true
	}
	if len(instance.TypeBindings) == 0 {
		return true
	}
	for i, arg := range typ.Arguments {
		if i >= len(instance.Class.TypeParameters) {
			break
		}
		if arg == nil || arg.Operator != "" || arg.Name == "" {
			continue
		}
		paramName := instance.Class.TypeParameters[i]
		bound, ok := instance.TypeBindings[paramName]
		if !ok || bound == "" {
			continue
		}
		if !typeNamesEqual(bound, arg.Name) {
			return false
		}
	}
	return true
}

func simpleTypeName(name string) string {
	if _, suffix, ok := strings.Cut(name, "."); ok {
		return suffix
	}
	return name
}

func typeNamesEqual(left, right string) bool {
	return strings.EqualFold(simpleTypeName(left), simpleTypeName(right))
}

func isCallableTypeName(name string) bool {
	return strings.EqualFold(name, "func") || strings.EqualFold(name, "callable") || strings.EqualFold(name, "function")
}

func isGeneratorTypeName(name string) bool {
	return strings.EqualFold(name, "generator") || strings.EqualFold(name, "iterable")
}

// descriptiveTypeName returns a type name including element types where detectable,
// e.g. "list<string>" instead of "list", "dict<string,int>" instead of "dict".
// For reified user-defined generic class instances it also unspools the
// recorded TypeBindings - "Container<Sub>" rather than the bare "Container" -
// so error messages about invariant-parameter mismatches surface the
// caller's actual binding rather than just the class name.
func descriptiveTypeName(value runtime.Value) string {
	switch v := value.(type) {
	case *runtime.List:
		if len(v.Elements) > 0 {
			return "list<" + v.Elements[0].TypeName() + ">"
		}
		return "list"
	case runtime.Set:
		for _, entry := range v.Elements {
			return "set<" + entry.Value.TypeName() + ">"
		}
		return "set"
	case runtime.Dict:
		for _, entry := range v.Entries {
			return "dict<" + entry.Key.TypeName() + "," + entry.Value.TypeName() + ">"
		}
		return "dict"
	case *runtime.Instance:
		if v == nil || v.Class == nil || len(v.Class.TypeParameters) == 0 || len(v.TypeBindings) == 0 {
			return value.TypeName()
		}
		parts := make([]string, 0, len(v.Class.TypeParameters))
		for _, p := range v.Class.TypeParameters {
			if bound, ok := v.TypeBindings[p]; ok && bound != "" {
				parts = append(parts, bound)
			}
		}
		if len(parts) == 0 {
			return value.TypeName()
		}
		return v.Class.Name + "<" + strings.Join(parts, ", ") + ">"
	}
	return value.TypeName()
}

func valueMatchesFunctionTypeRef(fn runtime.Function, value runtime.Value, typ *ast.TypeRef) bool {
	return matchValueToTypeRef(functionTypeParameterSetOrNil(fn), value, typ)
}

// matchValueToTypeRef is the recursive implementation of collection element type checking.
// typeParams is the pre-computed generic type parameter set (nil for non-generic contexts); a nil
// map is safe - Go map lookups on nil maps return the zero value.
func matchValueToTypeRef(typeParams map[string]bool, value runtime.Value, typ *ast.TypeRef) bool {
	if typ == nil || typ.Name == "any" {
		return true
	}
	if typ.Operator == "|" {
		return matchValueToTypeRef(typeParams, value, typ.Left) || matchValueToTypeRef(typeParams, value, typ.Right)
	}
	if typ.Operator == "&" {
		return matchValueToTypeRef(typeParams, value, typ.Left) && matchValueToTypeRef(typeParams, value, typ.Right)
	}
	if typ.Operator != "" {
		return true
	}
	// A null value is assignable to any nullable type, regardless of element
	// parameterisation. The element check below would otherwise dereference
	// the null as a List/Dict/Set and panic in the VM.
	if _, isNull := value.(runtime.Null); isNull {
		return typ.Nullable
	}
	if typeParams[strings.ToLower(typ.Name)] {
		return true
	}
	typeName := simpleTypeName(typ.Name)
	if typ.ListAlias || typeName == "list" {
		if value.TypeName() != "list" {
			return false
		}
		var elemType *ast.TypeRef
		if len(typ.Arguments) > 0 {
			elemType = typ.Arguments[0]
		} else if typ.ListAlias && typ.Name != "" && !strings.EqualFold(typ.Name, "list") {
			// T[] syntax: element type is the Name field (e.g. int[] → element type is int)
			elemType = &ast.TypeRef{Token: typ.Token, Name: typ.Name}
		}
		if elemType != nil && !typeParams[strings.ToLower(elemType.Name)] {
			lst := value.(*runtime.List)
			for _, elem := range lst.Elements {
				if !matchValueToTypeRef(typeParams, elem, elemType) {
					return false
				}
			}
		}
		return true
	}
	if typeName == "set" {
		if value.TypeName() != "set" {
			return false
		}
		if len(typ.Arguments) > 0 && !typeParams[strings.ToLower(typ.Arguments[0].Name)] {
			s := value.(runtime.Set)
			for _, entry := range s.Elements {
				if !matchValueToTypeRef(typeParams, entry.Value, typ.Arguments[0]) {
					return false
				}
			}
		}
		return true
	}
	if typeName == "dict" {
		if value.TypeName() != "dict" {
			return false
		}
		if len(typ.Arguments) >= 2 {
			keyIsTP := typeParams[strings.ToLower(typ.Arguments[0].Name)]
			valIsTP := typeParams[strings.ToLower(typ.Arguments[1].Name)]
			d := value.(runtime.Dict)
			for _, entry := range d.Entries {
				if !keyIsTP && !matchValueToTypeRef(typeParams, entry.Key, typ.Arguments[0]) {
					return false
				}
				if !valIsTP && !matchValueToTypeRef(typeParams, entry.Value, typ.Arguments[1]) {
					return false
				}
			}
		}
		return true
	}
	return valueMatchesTypeRef(value, typ)
}

// collectionMismatchSuffix returns a detail string like " (element at index 1 is string)"
// describing the first element that fails the type check. Returns "" when no mismatch is found
// or the type has no element-type arguments to check against.
func collectionMismatchSuffix(value runtime.Value, typ *ast.TypeRef) string {
	if typ == nil {
		return ""
	}
	switch v := value.(type) {
	case *runtime.List:
		var elemType *ast.TypeRef
		if len(typ.Arguments) > 0 {
			elemType = typ.Arguments[0]
		} else if typ.ListAlias && typ.Name != "" && !strings.EqualFold(typ.Name, "list") {
			elemType = &ast.TypeRef{Token: typ.Token, Name: typ.Name}
		}
		if elemType == nil {
			return ""
		}
		for i, elem := range v.Elements {
			if !matchValueToTypeRef(nil, elem, elemType) {
				return fmt.Sprintf(" (element at index %d is %s)", i, elem.TypeName())
			}
		}
	case runtime.Set:
		if len(typ.Arguments) == 0 {
			return ""
		}
		for _, entry := range v.Elements {
			if !matchValueToTypeRef(nil, entry.Value, typ.Arguments[0]) {
				return fmt.Sprintf(" (element %s is %s)", entry.Value.Inspect(), entry.Value.TypeName())
			}
		}
	case runtime.Dict:
		if len(typ.Arguments) < 2 {
			return ""
		}
		for _, entry := range v.Entries {
			if !matchValueToTypeRef(nil, entry.Value, typ.Arguments[1]) {
				return fmt.Sprintf(" (value for key %s is %s)", entry.Key.Inspect(), entry.Value.TypeName())
			}
		}
	}
	return ""
}

func typeRefUsesFunctionTypeParameter(fn runtime.Function, typ *ast.TypeRef) bool {
	if typ == nil {
		return false
	}
	params := functionTypeParameterSetOrNil(fn)
	if params == nil {
		return false
	}
	return typeRefUsesTypeParameter(typ, params)
}

func functionTypeParameterSet(fn runtime.Function) map[string]bool {
	params := map[string]bool{}
	for _, name := range fn.TypeParameters {
		params[strings.ToLower(name)] = true
	}
	return params
}

func functionTypeParameterSetOrNil(fn runtime.Function) map[string]bool {
	if len(fn.TypeParameters) == 0 {
		return nil
	}
	return functionTypeParameterSet(fn)
}

func typeRefUsesTypeParameter(typ *ast.TypeRef, params map[string]bool) bool {
	if typ == nil {
		return false
	}
	if params[strings.ToLower(typ.Name)] {
		return true
	}
	for _, arg := range typ.Arguments {
		if typeRefUsesTypeParameter(arg, params) {
			return true
		}
	}
	return typeRefUsesTypeParameter(typ.Left, params) || typeRefUsesTypeParameter(typ.Right, params)
}

func (e *Evaluator) evalFunctionCallArguments(fn runtime.Function, call *ast.CallExpression, env *runtime.Environment) ([]runtime.Value, error) {
	hasNamed := false
	for _, arg := range call.Arguments {
		if arg.Name != nil || arg.Spread {
			hasNamed = true
			break
		}
	}
	if !hasNamed {
		return e.evalCallArgumentsWithHints(call, env, fn.Parameters)
	}
	provided, err := e.evalDetailedCallArguments(call, env)
	if err != nil {
		return nil, err
	}
	args, ok := bindEvaluatedFunctionCallArguments(fn, provided)
	if !ok {
		return nil, fmt.Errorf("no matching arguments for %s", fn.Name)
	}
	return args, nil
}

func (e *Evaluator) evalCallArgumentsWithHints(call *ast.CallExpression, env *runtime.Environment, params []ast.Parameter) ([]runtime.Value, error) {
	args := make([]runtime.Value, 0, len(call.Arguments))
	paramIdx := 0
	for _, arg := range call.Arguments {
		var expected *ast.TypeRef
		if paramIdx < len(params) {
			p := params[paramIdx]
			if p.Type != nil && p.Type.Operator == "" && p.Type.Name != "any" {
				expected = p.Type
			}
			if !arg.Spread {
				paramIdx++
			}
		}
		value, err := e.evalExpressionWithExpectedType(arg.Value, env, expected)
		if err != nil {
			return nil, err
		}
		if arg.Spread {
			list, ok := value.(*runtime.List)
			if !ok {
				return nil, fmt.Errorf("spread argument must be a list")
			}
			args = append(args, list.Elements...)
			continue
		}
		args = append(args, value)
	}
	return args, nil
}

func (e *Evaluator) applyFunction(fn runtime.Function, args []runtime.Value) (runtime.Value, error) {
	return e.applyFunctionWithThis(fn, args, nil)
}

// applyFunctionWithTypeArgs runs a generic function with type-parameter
// bindings pre-seeded from the call site's explicit `<TypeArgs>` clause.
// These bindings take strict priority over the per-call inference loop in
// applyFunctionWithThisSync: an `assertIs<int>(...)` call binds T to int
// regardless of what the argument's runtime type is, and the parameter
// type check then resolves T to int through the planted callEnv binding.
// When the call site has no explicit type arguments, falls through to the
// normal inference path.
// inferGenericBindingsFromTypeRef walks a parameter's TypeRef tree
// against the concrete arg value to discover bindings for any leaf
// type-parameter references. Mirrors VM.inferGenericBindingsFromSpec
// so the same nested-generic cases bind identically in both backends.
// Direct type-param leaves like `T` bind from arg.TypeName(); nested
// container shapes recurse into the inner spec against the container's
// first element/entry.
func (e *Evaluator) inferGenericBindingsFromTypeRef(spec *ast.TypeRef, value runtime.Value, typeParamSet map[string]bool, callEnv *runtime.Environment, this *runtime.Instance) {
	if spec == nil || spec.Operator != "" || value == nil {
		return
	}
	if len(spec.Arguments) == 0 {
		if !typeParamSet[strings.ToLower(spec.Name)] {
			return
		}
		e.recordInferredBinding(spec.Name, value.TypeName(), callEnv, this)
		return
	}
	switch strings.ToLower(spec.Name) {
	case "list":
		list, ok := value.(*runtime.List)
		if !ok || len(list.Elements) == 0 || len(spec.Arguments) == 0 {
			return
		}
		e.inferGenericBindingsFromTypeRef(spec.Arguments[0], list.Elements[0], typeParamSet, callEnv, this)
	case "set":
		set, ok := value.(runtime.Set)
		if !ok || len(set.Elements) == 0 || len(spec.Arguments) == 0 {
			return
		}
		for _, entry := range set.Elements {
			e.inferGenericBindingsFromTypeRef(spec.Arguments[0], entry.Value, typeParamSet, callEnv, this)
			break
		}
	case "dict":
		if len(spec.Arguments) != 2 {
			return
		}
		d, ok := value.(runtime.Dict)
		if !ok || len(d.Entries) == 0 {
			return
		}
		for _, entry := range d.Entries {
			e.inferGenericBindingsFromTypeRef(spec.Arguments[0], entry.Key, typeParamSet, callEnv, this)
			e.inferGenericBindingsFromTypeRef(spec.Arguments[1], entry.Value, typeParamSet, callEnv, this)
			break
		}
	}
}

// recordInferredBinding stores a discovered (type-param-name, type-name)
// binding into the call environment and, for constructor calls, into
// the instance's TypeBindings so reflect.typeBindings can surface it.
// "skip if already bound" semantics match the legacy non-recursive
// code so explicit `<T=...>` args planted by the caller win.
func (e *Evaluator) recordInferredBinding(name, typeName string, callEnv *runtime.Environment, this *runtime.Instance) {
	if name == "" || typeName == "" {
		return
	}
	if _, already := callEnv.GetTypeBinding(name); !already {
		callEnv.DefineTypeBinding(name, typeName)
	}
	if this == nil || this.Class == nil {
		return
	}
	if _, alreadyBound := this.TypeBindings[name]; alreadyBound {
		return
	}
	for _, cp := range this.Class.TypeParameters {
		if strings.EqualFold(cp, name) {
			if this.TypeBindings == nil {
				this.TypeBindings = map[string]string{}
			}
			this.TypeBindings[name] = typeName
			return
		}
	}
}

func (e *Evaluator) applyFunctionWithTypeArgs(fn runtime.Function, args []runtime.Value, typeArgs []*ast.TypeRef) (runtime.Value, error) {
	if len(typeArgs) == 0 || len(fn.TypeParameters) == 0 {
		return e.applyFunction(fn, args)
	}
	merged := map[string]string{}
	for k, v := range fn.TypeBindings {
		merged[k] = v
	}
	for i, t := range typeArgs {
		if i >= len(fn.TypeParameters) {
			break
		}
		if t == nil || t.Operator != "" || t.Name == "" {
			continue
		}
		merged[fn.TypeParameters[i]] = t.Name
	}
	clone := fn
	clone.TypeBindings = merged
	return e.applyFunction(clone, args)
}

func (e *Evaluator) applyFunctionWithThis(fn runtime.Function, args []runtime.Value, this *runtime.Instance) (runtime.Value, error) {
	if fn.IsGenerator {
		return e.lazyGenerator(fn, args, this), nil
	}
	if fn.Async {
		return e.startAsyncFunction(fn, args, this), nil
	}
	if fn.Native != nil {
		return fn.Native(this, args)
	}
	return e.applyFunctionWithThisSync(fn, args, this)
}

func (e *Evaluator) applyFunctionWithThisSync(fn runtime.Function, args []runtime.Value, this *runtime.Instance) (runtime.Value, error) {
	if fn.IsGenerator {
		return e.lazyGenerator(fn, args, this), nil
	}
	if fn.Native != nil {
		return fn.Native(this, args)
	}
	maxDepth := e.maxCallDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxCallDepth
	}
	if e.callDepth >= maxDepth {
		return nil, thrownError{value: e.withTrace(runtime.Error{Class: "FatalError", Message: fmt.Sprintf("maximum call depth exceeded (%d)", maxDepth), Fatal: true})}
	}
	e.callDepth++
	defer func() {
		e.callDepth--
	}()
	if fn.OwnerClass != nil {
		e.classStack = append(e.classStack, fn.OwnerClass)
		defer func() {
			e.classStack = e.classStack[:len(e.classStack)-1]
		}()
	}
	isVariadic := len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Variadic
	if !isVariadic && len(args) > len(fn.Parameters) {
		return nil, fmt.Errorf("function expects at most %d arguments, got %d", len(fn.Parameters), len(args))
	}
	if err := functionArgumentsMatchError(fn, args); err != nil {
		return nil, err
	}
	callEnv := runtime.NewEnclosedEnvironment(fn.Env)
	if this != nil {
		if err := callEnv.Define("this", this, false); err != nil {
			return nil, err
		}
	}
	// Plant the captured type bindings from the function's creation
	// site (the outer generic frame for a lambda or a generic-function
	// reference). Functions without TypeBindings (defined outside any
	// generic scope) skip this step.
	for name, typeName := range fn.TypeBindings {
		callEnv.DefineTypeBinding(name, typeName)
	}
	// Inherit class type bindings from receiver instance (lower priority - set first so
	// that inferred own-param bindings below can override if needed, but class params win
	// for methods since the instance binding was set at construction time).
	if this != nil {
		for name, typeName := range this.TypeBindings {
			callEnv.DefineTypeBinding(name, typeName)
		}
	}
	// Infer function type parameter bindings from params+args.
	if len(fn.TypeParameters) > 0 {
		typeParamSet := map[string]bool{}
		for _, p := range fn.TypeParameters {
			typeParamSet[strings.ToLower(p)] = true
		}
		for i, param := range fn.Parameters {
			if i >= len(args) || param.Type == nil {
				break
			}
			e.inferGenericBindingsFromTypeRef(param.Type, args[i], typeParamSet, callEnv, this)
		}
	}
	if err := e.checkTypeParamConstraints(fn, callEnv); err != nil {
		return nil, err
	}
	for i, param := range fn.Parameters {
		var value runtime.Value
		if param.Variadic {
			variadicElements := make([]runtime.Value, 0)
			if i < len(args) {
				variadicElements = args[i:]
			}
			value = &runtime.List{Elements: variadicElements}
		} else if i < len(args) && args[i] != nil {
			value = args[i]
		} else if param.Default != nil {
			evaluated, err := e.evalExpression(param.Default, callEnv)
			if err != nil {
				return nil, err
			}
			value = evaluated
		} else {
			return nil, fmt.Errorf("missing argument %q", param.Name.Value)
		}
		if param.Const {
			value = runtime.FreezeShallowCopy(value)
		}
		if err := callEnv.Define(param.Name.Value, value, false); err != nil {
			return nil, err
		}
	}
	e.pushDeferFrame()
	e.pushFunction(&fn, callEnv)
	defer e.popFunction()
	sig, err := e.evalBlock(fn.Body, callEnv)
	if err != nil {
		return nil, err
	}
	sig, err = e.runAndPopDefers(sig)
	if err != nil {
		return nil, err
	}
	if sig.exited {
		return exitValue{code: sig.exitCode}, nil
	}
	if sig.kind == "return" {
		return sig.value, nil
	}
	if sig.kind == "break" || sig.kind == "continue" {
		return nil, fmt.Errorf("%s cannot leave a function body", sig.kind)
	}
	if sig.kind == "generatorClosed" {
		return runtime.Null{}, nil
	}
	if sig.kind == "throw" && sig.thrown != nil {
		return nil, thrownError{value: *sig.thrown}
	}
	return runtime.Null{}, nil
}

func (e *Evaluator) lazyGenerator(fn runtime.Function, args []runtime.Value, this *runtime.Instance) *runtime.Generator {
	items := make(chan generatorItem)
	doneCh := make(chan struct{})
	stop := sync.Once{}
	start := sync.Once{}
	var pendingErr error
	closed := false
	return runtime.NewClosableGenerator(func() (runtime.Value, bool, error) {
		start.Do(func() {
			// Construct the child evaluator on the parent goroutine to keep
			// concurrent generator starts race-clean on package-level
			// native callbacks.
			child := e.childForCallback()
			runner := fn
			runner.IsGenerator = false
			child.pushYieldChannel(items, doneCh)
			go func() {
				defer close(items)
				defer child.popYieldFrame()
				value, err := child.applyFunctionWithThisSync(runner, args, this)
				if err != nil {
					select {
					case items <- generatorItem{err: err}:
					case <-doneCh:
					}
					return
				}
				if exit, ok := value.(exitValue); ok {
					select {
					case items <- generatorItem{err: fmt.Errorf("generator attempted to exit with code %d", exit.code)}:
					case <-doneCh:
					}
				}
			}()
		})
		if closed {
			return nil, false, pendingErr
		}
		item, ok := <-items
		if !ok {
			closed = true
			return nil, false, nil
		}
		if item.err != nil {
			closed = true
			pendingErr = item.err
			return nil, false, item.err
		}
		return item.value, true, nil
	}, func() {
		stop.Do(func() {
			close(doneCh)
		})
	})
}

func (e *Evaluator) startAsyncFunction(fn runtime.Function, args []runtime.Value, this *runtime.Instance) *runtime.Task {
	task := runtime.NewTask()
	// Build the child evaluator on the parent's goroutine; spawning it
	// inside the goroutine races with parallel async calls on writes to
	// package-level native callbacks (InstanceInvoker, ClassDeserializer).
	child := e.childForCallback()
	runtime.AsyncEnter()
	go func() {
		defer runtime.AsyncLeave()
		value, err := child.applyFunctionWithThisSync(fn, args, this)
		if exit, ok := value.(exitValue); ok && err == nil {
			err = fmt.Errorf("async function attempted to exit with code %d", exit.code)
			value = runtime.Null{}
		}
		task.Complete(value, err)
	}()
	return task
}

func awaitValue(value runtime.Value) (runtime.Value, error) {
	task, ok := value.(*runtime.Task)
	if !ok {
		return value, nil
	}
	result := task.Await()
	if result.Err != nil {
		return nil, result.Err
	}
	if result.Value == nil {
		return runtime.Null{}, nil
	}
	return result.Value, nil
}

func bindThis(env *runtime.Environment, this *runtime.Instance) *runtime.Environment {
	bound := runtime.NewEnclosedEnvironment(env)
	_ = bound.Define("this", this, false)
	return bound
}

func (e *Evaluator) pushFunction(fn *runtime.Function, env *runtime.Environment) {
	line := 0
	if fn != nil && fn.Body != nil {
		line = fn.Body.Token.Line
	}
	name := "<anonymous>"
	if fn != nil && fn.Name != "" {
		name = fn.Name
	}
	e.callStack = append(e.callStack, evalFrame{name: name, line: line, fn: fn, env: env})
}

func (e *Evaluator) popFunction() {
	if len(e.callStack) > 0 {
		e.callStack = e.callStack[:len(e.callStack)-1]
	}
}

func (e *Evaluator) currentFunction() *runtime.Function {
	if len(e.callStack) == 0 {
		return nil
	}
	return e.callStack[len(e.callStack)-1].fn
}

func (e *Evaluator) callStackTrace() string {
	var sb strings.Builder
	for i := len(e.callStack) - 1; i >= 0; i-- {
		frame := e.callStack[i]
		if frame.line > 0 {
			fmt.Fprintf(&sb, "\n  at %s (line %d)", frame.name, frame.line)
		} else {
			fmt.Fprintf(&sb, "\n  at %s", frame.name)
		}
	}
	sb.WriteString("\n  at <top level>")
	return sb.String()
}

func (e *Evaluator) withTrace(err runtime.Error) runtime.Error {
	if err.StackTrace == "" {
		err.StackTrace = e.callStackTrace()
	}
	return err
}

func (e *Evaluator) pushYieldFrame() {
	e.yieldFrames = append(e.yieldFrames, &yieldFrame{})
}

func (e *Evaluator) pushYieldChannel(ch chan generatorItem, done <-chan struct{}) {
	e.yieldFrames = append(e.yieldFrames, &yieldFrame{ch: ch, done: done})
}

func (e *Evaluator) appendYield(value runtime.Value) (bool, error) {
	if len(e.yieldFrames) == 0 {
		return false, fmt.Errorf("yield can only be used inside a generator function")
	}
	index := len(e.yieldFrames) - 1
	frame := e.yieldFrames[index]
	if frame.ch != nil {
		select {
		case frame.ch <- generatorItem{value: value}:
			return false, nil
		case <-frame.done:
			return true, nil
		}
	}
	frame.values = append(frame.values, value)
	return false, nil
}

func (e *Evaluator) popYieldFrame() []runtime.Value {
	if len(e.yieldFrames) == 0 {
		return nil
	}
	index := len(e.yieldFrames) - 1
	values := e.yieldFrames[index].values
	e.yieldFrames = e.yieldFrames[:index]
	return values
}

func (e *Evaluator) pushDeferFrame() {
	e.deferFrames = append(e.deferFrames, &deferFrame{})
}

func (e *Evaluator) registerDefer(expr ast.Expression, env *runtime.Environment) {
	if len(e.deferFrames) == 0 {
		e.pushDeferFrame()
	}
	frame := e.deferFrames[len(e.deferFrames)-1]
	frame.calls = append(frame.calls, deferredCall{expr: expr, env: env})
}

func (e *Evaluator) runAndPopDefers(sig signal) (signal, error) {
	if len(e.deferFrames) == 0 {
		return sig, nil
	}
	frame := e.deferFrames[len(e.deferFrames)-1]
	e.deferFrames = e.deferFrames[:len(e.deferFrames)-1]
	for i := len(frame.calls) - 1; i >= 0; i-- {
		value, err := e.evalExpression(frame.calls[i].expr, frame.calls[i].env)
		if err != nil {
			return signal{}, err
		}
		if exit, ok := value.(exitValue); ok {
			return signal{exited: true, exitCode: exit.code}, nil
		}
	}
	return sig, nil
}

func (e *Evaluator) evalIncrement(expr ast.Expression, operator string, env *runtime.Environment) (runtime.Value, error) {
	ident, ok := expr.(*ast.Identifier)
	if !ok {
		return nil, fmt.Errorf("%s currently supports identifiers only", operator)
	}
	current, ok := env.Get(ident.Value)
	if !ok {
		return nil, fmt.Errorf("%q is not declared", ident.Value)
	}
	intValue, ok := current.(runtime.Int)
	if !ok {
		return nil, fmt.Errorf("%s currently supports int values only", operator)
	}
	next := new(big.Int).Set(intValue.Value)
	if operator == "++" {
		next.Add(next, big.NewInt(1))
	} else {
		next.Sub(next, big.NewInt(1))
	}
	value := runtime.Int{Value: next}
	if err := env.Assign(ident.Value, value); err != nil {
		return nil, err
	}
	return value, nil
}

type exitValue struct {
	code int
}

func (v exitValue) TypeName() string { return "exit" }
func (v exitValue) Inspect() string  { return fmt.Sprintf("exit(%d)", v.code) }

func selectorName(expr ast.Expression) (string, string, bool) {
	selector, ok := expr.(*ast.SelectorExpression)
	if !ok {
		return "", "", false
	}
	ident, ok := selector.Object.(*ast.Identifier)
	if !ok {
		return "", "", false
	}
	return ident.Value, selector.Name.Value, true
}

func singlePrintableValue(call *ast.CallExpression, args []runtime.Value) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	return args[0].Inspect(), nil
}

func singleIntValue(call *ast.CallExpression, args []runtime.Value) (int64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	arg, ok := args[0].(runtime.Int)
	if !ok {
		return 0, fmt.Errorf("%s expects an integer argument", call.Callee.String())
	}
	if !arg.Value.IsInt64() {
		return 0, fmt.Errorf("%s integer argument is out of int64 range", call.Callee.String())
	}
	return arg.Value.Int64(), nil
}

func singleStringValue(call *ast.CallExpression, args []runtime.Value) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects exactly one argument", call.Callee.String())
	}
	arg, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s expects a string argument", call.Callee.String())
	}
	return arg.Value, nil
}

func (e *Evaluator) evalPrefix(operator string, right runtime.Value) (runtime.Value, error) {
	if value, ok, err := e.evalPrefixOperatorMethod(operator, right); ok || err != nil {
		return value, err
	}
	switch operator {
	case "!":
		value, ok := right.(runtime.Bool)
		if !ok {
			return nil, fmt.Errorf("! expects bool, got %s", right.TypeName())
		}
		return runtime.Bool{Value: !value.Value}, nil
	case "-":
		switch value := right.(type) {
		case runtime.Int:
			return runtime.Int{Value: new(big.Int).Neg(value.Value)}, nil
		case runtime.Decimal:
			return runtime.Decimal{Value: new(big.Rat).Neg(value.Value)}, nil
		case runtime.Float:
			return runtime.Float{Value: -value.Value}, nil
		default:
			return nil, fmt.Errorf("- expects numeric value, got %s", right.TypeName())
		}
	case "~":
		value, ok := right.(runtime.Int)
		if !ok {
			return nil, fmt.Errorf("~ expects int, got %s", right.TypeName())
		}
		return runtime.Int{Value: new(big.Int).Not(value.Value)}, nil
	default:
		return nil, fmt.Errorf("unsupported prefix operator %q", operator)
	}
}

func (e *Evaluator) evalPrefixOperatorMethod(operator string, right runtime.Value) (runtime.Value, bool, error) {
	instance, ok := right.(*runtime.Instance)
	if !ok {
		return nil, false, nil
	}
	name, ok := prefixOperatorMethodName(operator)
	if !ok {
		return nil, false, nil
	}
	method, ok := lookupMethod(instance.Class, name)
	if !ok {
		return nil, false, nil
	}
	value, err := e.applyFunctionWithThis(method, nil, instance)
	if err != nil {
		return nil, true, err
	}
	return value, true, nil
}

func prefixOperatorMethodName(operator string) (string, bool) {
	switch operator {
	case "!":
		return "__not", true
	case "-":
		return "__neg", true
	case "~":
		return "__bitnot", true
	default:
		return "", false
	}
}

func (e *Evaluator) evalInfix(operator string, left runtime.Value, right runtime.Value) (runtime.Value, error) {
	if operator == "&&" || operator == "||" || operator == "xor" {
		return evalBoolInfix(operator, left, right)
	}
	if operator == "==" || operator == "!=" {
		equal, err := e.valuesEqual(left, right)
		if err != nil {
			return nil, err
		}
		if operator == "!=" {
			equal = !equal
		}
		return runtime.Bool{Value: equal}, nil
	}
	if operator == "is" || operator == "is not" {
		same := valuesIdentical(left, right)
		if operator == "is not" {
			same = !same
		}
		return runtime.Bool{Value: same}, nil
	}
	if value, ok, err := e.evalOperatorMethod(operator, left, right); ok || err != nil {
		return value, err
	}
	if left.TypeName() == "string" && right.TypeName() == "string" && operator == "+" {
		return runtime.String{Value: left.(runtime.String).Value + right.(runtime.String).Value}, nil
	}
	if isComparison(operator) {
		return evalComparison(operator, left, right)
	}
	return evalNumericInfix(operator, left, right)
}

func (e *Evaluator) evalOperatorMethod(operator string, left runtime.Value, right runtime.Value) (runtime.Value, bool, error) {
	instance, ok := left.(*runtime.Instance)
	if !ok {
		return nil, false, nil
	}
	name, ok := operatorMethodName(operator)
	if !ok {
		return nil, false, nil
	}
	method, ok := lookupMethod(instance.Class, name)
	if !ok {
		return nil, false, nil
	}
	value, err := e.applyFunctionWithThis(method, []runtime.Value{right}, instance)
	if err != nil {
		return nil, true, err
	}
	if isComparison(operator) {
		if _, ok := value.(runtime.Bool); !ok {
			return nil, true, fmt.Errorf("%s.%s must return bool", instance.Class.Name, name)
		}
	}
	return value, true, nil
}

func operatorMethodName(operator string) (string, bool) {
	switch operator {
	case "+":
		return "__add", true
	case "-":
		return "__sub", true
	case "*":
		return "__mul", true
	case "/":
		return "__div", true
	case "//":
		return "__intdiv", true
	case "%":
		return "__mod", true
	case "**":
		return "__pow", true
	case "<":
		return "__lt", true
	case "<=":
		return "__lte", true
	case ">":
		return "__gt", true
	case ">=":
		return "__gte", true
	case "&":
		return "__bitand", true
	case "|":
		return "__bitor", true
	case "^":
		return "__bitxor", true
	case "<<":
		return "__lshift", true
	case ">>":
		return "__rshift", true
	default:
		return "", false
	}
}

func (e *Evaluator) valuesEqual(left runtime.Value, right runtime.Value) (bool, error) {
	if ev, ok := left.(runtime.EnumVariant); ok {
		rv, ok := right.(runtime.EnumVariant)
		if !ok || ev.Enum != rv.Enum || ev.Variant != rv.Variant || len(ev.Fields) != len(rv.Fields) {
			return false, nil
		}
		for i, f := range ev.Fields {
			eq, err := e.valuesEqual(f, rv.Fields[i])
			if !eq || err != nil {
				return eq, err
			}
		}
		return true, nil
	}
	if instance, ok := left.(*runtime.Instance); ok {
		if method, ok := lookupMethod(instance.Class, "__eq"); ok {
			value, err := e.applyFunctionWithThis(method, []runtime.Value{right}, instance)
			if err != nil {
				return false, err
			}
			result, ok := value.(runtime.Bool)
			if !ok {
				return false, fmt.Errorf("%s.__eq must return bool", instance.Class.Name)
			}
			return result.Value, nil
		}
		other, ok := right.(*runtime.Instance)
		if !ok || other.Class != instance.Class {
			return false, nil
		}
		if len(instance.Fields) != len(other.Fields) {
			return false, nil
		}
		for name, leftField := range instance.Fields {
			rightField, ok := other.Fields[name]
			if !ok {
				return false, nil
			}
			equal, err := e.valuesEqual(leftField, rightField)
			if err != nil {
				return false, err
			}
			if !equal {
				return false, nil
			}
		}
		return true, nil
	}
	return primitiveEqual(left, right), nil
}

func valuesIdentical(left runtime.Value, right runtime.Value) bool {
	switch l := left.(type) {
	case *runtime.Instance:
		r, ok := right.(*runtime.Instance)
		return ok && l == r
	case *runtime.Class:
		r, ok := right.(*runtime.Class)
		return ok && l == r
	case runtime.Null:
		_, ok := right.(runtime.Null)
		return ok
	default:
		return primitiveEqual(left, right)
	}
}

func primitiveEqual(left runtime.Value, right runtime.Value) bool {
	switch leftValue := left.(type) {
	case runtime.Null:
		_, ok := right.(runtime.Null)
		return ok
	case runtime.Bool:
		rightValue, ok := right.(runtime.Bool)
		return ok && leftValue.Value == rightValue.Value
	case runtime.SmallInt:
		// Cross-comparison with Int for the case where a SmallInt
		// arrived from a native source (e.g. JSON parser) and the
		// other side is an evaluator-built Int. Same int value
		// compares equal regardless of representation.
		if rightSmall, ok := right.(runtime.SmallInt); ok {
			return leftValue.Value == rightSmall.Value
		}
		if rightBig, ok := right.(runtime.Int); ok {
			return rightBig.Value.IsInt64() && rightBig.Value.Int64() == leftValue.Value
		}
		return false
	case runtime.Int:
		if rightValue, ok := right.(runtime.Int); ok {
			return leftValue.Value.Cmp(rightValue.Value) == 0
		}
		if rightSmall, ok := right.(runtime.SmallInt); ok {
			return leftValue.Value.IsInt64() && leftValue.Value.Int64() == rightSmall.Value
		}
		return false
	case runtime.Decimal:
		rightValue, ok := right.(runtime.Decimal)
		return ok && leftValue.Value.Cmp(rightValue.Value) == 0
	case runtime.Float:
		rightValue, ok := right.(runtime.Float)
		return ok && leftValue.Value == rightValue.Value
	case runtime.String:
		rightValue, ok := right.(runtime.String)
		return ok && leftValue.Value == rightValue.Value
	case runtime.Bytes:
		rightValue, ok := right.(runtime.Bytes)
		return ok && bytes.Equal(leftValue.Value, rightValue.Value)
	case runtime.DateTimeInstant:
		rightValue, ok := right.(runtime.DateTimeInstant)
		return ok && leftValue == rightValue
	case runtime.DateTimeDuration:
		rightValue, ok := right.(runtime.DateTimeDuration)
		return ok && leftValue == rightValue
	case runtime.DateTimeZone:
		rightValue, ok := right.(runtime.DateTimeZone)
		return ok && leftValue == rightValue
	case runtime.URLValue:
		rightValue, ok := right.(runtime.URLValue)
		return ok && leftValue == rightValue
	case runtime.HTTPHeaders:
		rightValue, ok := right.(runtime.HTTPHeaders)
		if !ok || len(leftValue.Values) != len(rightValue.Values) {
			return false
		}
		for key, values := range leftValue.Values {
			other := rightValue.Values[key]
			if len(values) != len(other) {
				return false
			}
			for i, value := range values {
				if value != other[i] {
					return false
				}
			}
		}
		return true
	case runtime.HTTPCookie:
		rightValue, ok := right.(runtime.HTTPCookie)
		return ok && leftValue == rightValue
	case runtime.TemplateValue:
		rightValue, ok := right.(runtime.TemplateValue)
		return ok && leftValue == rightValue
	case runtime.TemplateEngine:
		rightValue, ok := right.(runtime.TemplateEngine)
		return ok && leftValue == rightValue
	case *runtime.List:
		rightValue, ok := right.(*runtime.List)
		if !ok || len(leftValue.Elements) != len(rightValue.Elements) {
			return false
		}
		for i, element := range leftValue.Elements {
			if !primitiveEqual(element, rightValue.Elements[i]) {
				return false
			}
		}
		return true
	case runtime.Dict:
		rightValue, ok := right.(runtime.Dict)
		if !ok || len(leftValue.Entries) != len(rightValue.Entries) {
			return false
		}
		for key, entry := range leftValue.Entries {
			other, ok := rightValue.Entries[key]
			if !ok || !primitiveEqual(entry.Key, other.Key) || !primitiveEqual(entry.Value, other.Value) {
				return false
			}
		}
		return true
	case runtime.Set:
		rightValue, ok := right.(runtime.Set)
		if !ok || len(leftValue.Elements) != len(rightValue.Elements) {
			return false
		}
		for key, entry := range leftValue.Elements {
			other, ok := rightValue.Elements[key]
			if !ok || !primitiveEqual(entry.Value, other.Value) {
				return false
			}
		}
		return true
	case runtime.Range:
		rightValue, ok := right.(runtime.Range)
		return ok &&
			leftValue.Exclusive == rightValue.Exclusive &&
			leftValue.Start.Cmp(rightValue.Start) == 0 &&
			leftValue.End.Cmp(rightValue.End) == 0 &&
			leftValue.Step.Cmp(rightValue.Step) == 0
	case runtime.BytecodeFunction:
		rightValue, ok := right.(runtime.BytecodeFunction)
		return ok && leftValue.Module == rightValue.Module && leftValue.Name == rightValue.Name && leftValue.Index == rightValue.Index
	case runtime.BytecodeClass:
		rightValue, ok := right.(runtime.BytecodeClass)
		return ok && leftValue.Module == rightValue.Module && leftValue.Name == rightValue.Name && leftValue.Index == rightValue.Index
	case runtime.NativeObject:
		rightValue, ok := right.(runtime.NativeObject)
		return ok && leftValue == rightValue
	case runtime.Error:
		rightValue, ok := right.(runtime.Error)
		return ok && leftValue.Class == rightValue.Class && leftValue.Message == rightValue.Message
	case runtime.Type:
		switch rv := right.(type) {
		case runtime.Type:
			return leftValue.Name == rv.Name
		case *runtime.Class:
			return leftValue.Name == rv.Name
		case runtime.BytecodeClass:
			return leftValue.Name == rv.Name
		}
		return false
	case *runtime.Module:
		rightValue, ok := right.(*runtime.Module)
		return ok && leftValue == rightValue
	case *runtime.Class:
		switch rv := right.(type) {
		case *runtime.Class:
			return leftValue == rv
		case runtime.Type:
			return leftValue.Name == rv.Name
		}
		return false
	case *runtime.Interface:
		rightValue, ok := right.(*runtime.Interface)
		return ok && leftValue == rightValue
	case *runtime.Instance:
		rightValue, ok := right.(*runtime.Instance)
		return ok && leftValue == rightValue
	default:
		return false
	}
}

func (e *Evaluator) nativeTestAssertion(name string) func(*runtime.Instance, []runtime.Value) (runtime.Value, error) {
	return func(_ *runtime.Instance, args []runtime.Value) (runtime.Value, error) {
		if strings.EqualFold(name, "assertThrows") {
			return e.assertThrowsImpl(args)
		}
		if strings.EqualFold(name, "assertThrowsOf") {
			return e.assertThrowsOfImpl(args)
		}
		value, handled, err := runtime.RunTestAssertion(name, args)
		if !handled {
			return nil, fmt.Errorf("unknown test assertion %s", name)
		}
		return value, err
	}
}

// assertThrowsOfImpl asserts the callable raises an error whose class
// matches `expectedClass` (walking the parent chain like a catch
// clause). The class argument is either a class value or a string
// class name; the latter lets built-in error classes
// (RuntimeError, PermissionError, ...) be referenced without
// requiring them to be reified as runtime values. Optional third
// argument is a substring that must appear in the error message.
func (e *Evaluator) assertThrowsOfImpl(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("Test.assertThrowsOf expects (callable, classOrName[, expectedSubstring])")
	}
	fn, ok := args[0].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("Test.assertThrowsOf expects a callable as the first argument")
	}
	expectedClass, err := classNameFromValue(args[1])
	if err != nil {
		return nil, fmt.Errorf("Test.assertThrowsOf: %w", err)
	}
	var expectedSub string
	if len(args) == 3 {
		s, ok := args[2].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Test.assertThrowsOf: third argument must be a string substring")
		}
		expectedSub = s.Value
	}
	_, err = e.applyFunction(fn, nil)
	if err == nil {
		return nil, fmt.Errorf("expected callable to throw %s, but it returned normally", expectedClass)
	}
	actualClass := extractErrorClass(err)
	if !e.errorTypeMatches(actualClass, expectedClass) {
		return nil, fmt.Errorf("expected %s, got %s: %s", expectedClass, actualClass, err.Error())
	}
	if expectedSub != "" && !strings.Contains(err.Error(), expectedSub) {
		return nil, fmt.Errorf("expected error containing %q, got %q", expectedSub, err.Error())
	}
	return runtime.Null{}, nil
}

// classNameFromValue projects a class reference into its name.
// Accepts a string (for built-in error classes whose identifiers
// aren't reified as values) or a runtime class value (user-
// defined classes referenced by name in source).
func classNameFromValue(v runtime.Value) (string, error) {
	switch x := v.(type) {
	case runtime.String:
		return x.Value, nil
	case *runtime.Class:
		return x.Name, nil
	case runtime.BytecodeClass:
		return x.Name, nil
	}
	return "", fmt.Errorf("expected class value or class name string, got %s", v.TypeName())
}

// extractErrorClass pulls the carried Geblang class name out of a
// thrown error, falling back to "RuntimeError" when the wrapped
// value doesn't carry an explicit class.
func extractErrorClass(err error) string {
	var typed runtime.TypedError
	if errors.As(err, &typed) {
		return typed.ErrorClass()
	}
	return runtime.RecoverableErrorClass(err)
}

// assertThrowsImpl invokes the callable arg and asserts it raises.
// Signature: assertThrows(callable) or assertThrows(callable, expectedSubstring).
func (e *Evaluator) assertThrowsImpl(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("Test.assertThrows expects (callable[, expectedSubstring])")
	}
	fn, ok := args[0].(runtime.Function)
	if !ok {
		return nil, fmt.Errorf("Test.assertThrows expects a callable as the first argument")
	}
	var expectedSub string
	if len(args) == 2 {
		s, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("Test.assertThrows: second argument must be a string substring")
		}
		expectedSub = s.Value
	}
	_, err := e.applyFunction(fn, nil)
	if err == nil {
		return nil, fmt.Errorf("expected callable to throw, but it returned normally")
	}
	if expectedSub != "" && !strings.Contains(err.Error(), expectedSub) {
		return nil, fmt.Errorf("expected error containing %q, got %q", expectedSub, err.Error())
	}
	return runtime.Null{}, nil
}

func evalBoolInfix(operator string, left runtime.Value, right runtime.Value) (runtime.Value, error) {
	l, ok := left.(runtime.Bool)
	if !ok {
		return nil, fmt.Errorf("%s expects bool operands", operator)
	}
	r, ok := right.(runtime.Bool)
	if !ok {
		return nil, fmt.Errorf("%s expects bool operands", operator)
	}
	switch operator {
	case "&&":
		return runtime.Bool{Value: l.Value && r.Value}, nil
	case "||":
		return runtime.Bool{Value: l.Value || r.Value}, nil
	case "xor":
		return runtime.Bool{Value: l.Value != r.Value}, nil
	default:
		return nil, fmt.Errorf("unsupported bool operator %q", operator)
	}
}

func evalNumericInfix(operator string, left runtime.Value, right runtime.Value) (runtime.Value, error) {
	// Promote SmallInt to Int so the rest of this dispatcher (and
	// the cascading per-type infix paths) only has to handle the
	// big.Int representation. SmallInt arrives here from native
	// integer sources (json parser, csv, etc.) that produce the
	// runtime's interface-inline int. The conversion allocates
	// O(1) big.Int per arithmetic step; the storage benefit of
	// SmallInt at parse-time pays for it as long as the loop body
	// doesn't run arithmetic on every value.
	if s, ok := left.(runtime.SmallInt); ok {
		left = runtime.NewInt64(s.Value)
	}
	if s, ok := right.(runtime.SmallInt); ok {
		right = runtime.NewInt64(s.Value)
	}
	switch l := left.(type) {
	case runtime.Int:
		switch r := right.(type) {
		case runtime.Int:
			return evalIntInfix(operator, l, r)
		case runtime.Decimal:
			return evalDecimalInfix(operator, intToDecimal(l), r)
		}
	case runtime.Decimal:
		switch r := right.(type) {
		case runtime.Int:
			return evalDecimalInfix(operator, l, intToDecimal(r))
		case runtime.Decimal:
			return evalDecimalInfix(operator, l, r)
		}
	case runtime.Float:
		if r, ok := right.(runtime.Float); ok {
			return evalFloatInfix(operator, l, r)
		}
	}
	return nil, fmt.Errorf("unsupported operands for %s: %s and %s", operator, left.TypeName(), right.TypeName())
}

func evalIntInfix(operator string, left runtime.Int, right runtime.Int) (runtime.Value, error) {
	switch operator {
	case "+":
		return runtime.Int{Value: new(big.Int).Add(left.Value, right.Value)}, nil
	case "-":
		return runtime.Int{Value: new(big.Int).Sub(left.Value, right.Value)}, nil
	case "*":
		return runtime.Int{Value: new(big.Int).Mul(left.Value, right.Value)}, nil
	case "//":
		if right.Value.Sign() == 0 {
			return nil, fmt.Errorf("integer division by zero")
		}
		quotient, _ := intFloorDivMod(left.Value, right.Value)
		return runtime.Int{Value: quotient}, nil
	case "%":
		if right.Value.Sign() == 0 {
			return nil, fmt.Errorf("modulo by zero")
		}
		_, remainder := intFloorDivMod(left.Value, right.Value)
		return runtime.Int{Value: remainder}, nil
	case "/":
		return evalDecimalInfix("/", intToDecimal(left), intToDecimal(right))
	case "**":
		if !right.Value.IsInt64() || right.Value.Sign() < 0 {
			return nil, fmt.Errorf("int exponent must be a non-negative int64")
		}
		return runtime.Int{Value: new(big.Int).Exp(left.Value, big.NewInt(right.Value.Int64()), nil)}, nil
	case "&":
		return runtime.Int{Value: new(big.Int).And(left.Value, right.Value)}, nil
	case "|":
		return runtime.Int{Value: new(big.Int).Or(left.Value, right.Value)}, nil
	case "^":
		return runtime.Int{Value: new(big.Int).Xor(left.Value, right.Value)}, nil
	case "<<":
		if !right.Value.IsUint64() {
			return nil, fmt.Errorf("shift amount must be a non-negative int, got %s", right.Value.String())
		}
		return runtime.Int{Value: new(big.Int).Lsh(left.Value, uint(right.Value.Uint64()))}, nil
	case ">>":
		if !right.Value.IsUint64() {
			return nil, fmt.Errorf("shift amount must be a non-negative int, got %s", right.Value.String())
		}
		return runtime.Int{Value: new(big.Int).Rsh(left.Value, uint(right.Value.Uint64()))}, nil
	default:
		return nil, fmt.Errorf("unsupported int operator %q", operator)
	}
}

func evalDecimalInfix(operator string, left runtime.Decimal, right runtime.Decimal) (runtime.Value, error) {
	switch operator {
	case "+":
		return runtime.Decimal{Value: new(big.Rat).Add(left.Value, right.Value)}, nil
	case "-":
		return runtime.Decimal{Value: new(big.Rat).Sub(left.Value, right.Value)}, nil
	case "*":
		return runtime.Decimal{Value: new(big.Rat).Mul(left.Value, right.Value)}, nil
	case "/":
		if right.Value.Sign() == 0 {
			return nil, fmt.Errorf("decimal division by zero")
		}
		return runtime.Decimal{Value: new(big.Rat).Quo(left.Value, right.Value)}, nil
	case "//":
		/* Floor division: divide, then floor. Floor toward negative
		 * infinity, not toward zero (`-7 // 2 = -4`, not -3) so the
		 * sign of a non-zero remainder matches the divisor. Mirrors
		 * Python `//` and Geblang's int//int behaviour. The result
		 * is a Decimal with an integer numerator. */
		if right.Value.Sign() == 0 {
			return nil, fmt.Errorf("decimal division by zero")
		}
		q := new(big.Rat).Quo(left.Value, right.Value)
		num := new(big.Int).Set(q.Num())
		den := q.Denom()
		floored, _ := new(big.Int).QuoRem(num, den, new(big.Int))
		if q.Sign() < 0 && new(big.Int).Mul(floored, den).Cmp(num) != 0 {
			floored.Sub(floored, big.NewInt(1))
		}
		return runtime.Decimal{Value: new(big.Rat).SetInt(floored)}, nil
	case "%":
		if right.Value.Sign() == 0 {
			return nil, fmt.Errorf("decimal modulo by zero")
		}
		q := new(big.Rat).Quo(left.Value, right.Value)
		num := new(big.Int).Set(q.Num())
		den := q.Denom()
		floored, _ := new(big.Int).QuoRem(num, den, new(big.Int))
		if q.Sign() < 0 && new(big.Int).Mul(floored, den).Cmp(num) != 0 {
			floored.Sub(floored, big.NewInt(1))
		}
		floorRat := new(big.Rat).SetInt(floored)
		product := new(big.Rat).Mul(floorRat, right.Value)
		return runtime.Decimal{Value: new(big.Rat).Sub(left.Value, product)}, nil
	default:
		return nil, fmt.Errorf("unsupported decimal operator %q", operator)
	}
}

func evalFloatInfix(operator string, left runtime.Float, right runtime.Float) (runtime.Value, error) {
	switch operator {
	case "+":
		return runtime.Float{Value: left.Value + right.Value}, nil
	case "-":
		return runtime.Float{Value: left.Value - right.Value}, nil
	case "*":
		return runtime.Float{Value: left.Value * right.Value}, nil
	case "/":
		return runtime.Float{Value: left.Value / right.Value}, nil
	case "//":
		if right.Value == 0 {
			return nil, fmt.Errorf("float division by zero")
		}
		return runtime.Float{Value: math.Floor(left.Value / right.Value)}, nil
	case "%":
		if right.Value == 0 {
			return nil, fmt.Errorf("float modulo by zero")
		}
		return runtime.Float{Value: left.Value - math.Floor(left.Value/right.Value)*right.Value}, nil
	case "**":
		return runtime.Float{Value: math.Pow(left.Value, right.Value)}, nil
	default:
		return nil, fmt.Errorf("unsupported float operator %q", operator)
	}
}

func evalComparison(operator string, left runtime.Value, right runtime.Value) (runtime.Value, error) {
	cmp, err := compareValues(left, right)
	if err != nil {
		return nil, err
	}
	switch operator {
	case "<":
		return runtime.Bool{Value: cmp < 0}, nil
	case "<=":
		return runtime.Bool{Value: cmp <= 0}, nil
	case ">":
		return runtime.Bool{Value: cmp > 0}, nil
	case ">=":
		return runtime.Bool{Value: cmp >= 0}, nil
	default:
		return nil, fmt.Errorf("unsupported comparison operator %q", operator)
	}
}

func isTruthy(v runtime.Value) bool {
	b, ok := v.(runtime.Bool)
	return ok && b.Value
}

func (e *Evaluator) callValue(fn runtime.Value, args []runtime.Value) (runtime.Value, error) {
	switch f := fn.(type) {
	case runtime.Function:
		return e.applyFunction(f, args)
	case runtime.OverloadedFunction:
		for _, overload := range f.Overloads {
			if len(overload.Parameters) == len(args) {
				return e.applyFunction(overload, args)
			}
		}
		return nil, fmt.Errorf("no matching overload for %s with %d arguments", f.Name, len(args))
	case runtime.DecoratorTarget:
		if f.Callable != nil {
			return e.callValue(f.Callable, args)
		}
		return nil, fmt.Errorf("reflect target is not callable")
	case *runtime.Instance:
		method, ok := lookupMethod(f.Class, "__invoke")
		if !ok {
			return nil, fmt.Errorf("value is not callable")
		}
		return e.applyFunctionWithThis(method, args, f)
	default:
		return nil, fmt.Errorf("value is not callable")
	}
}

func (e *Evaluator) compareValues(left runtime.Value, right runtime.Value) (int, error) {
	return compareValues(left, right)
}

func compareValues(left runtime.Value, right runtime.Value) (int, error) {
	switch l := left.(type) {
	case runtime.SmallInt:
		if r, ok := right.(runtime.SmallInt); ok {
			if l.Value < r.Value {
				return -1, nil
			}
			if l.Value > r.Value {
				return 1, nil
			}
			return 0, nil
		}
		lb := big.NewInt(l.Value)
		if r, ok := right.(runtime.Int); ok {
			return lb.Cmp(r.Value), nil
		}
		if r, ok := right.(runtime.Decimal); ok {
			return new(big.Rat).SetInt(lb).Cmp(r.Value), nil
		}
	case runtime.Int:
		switch r := right.(type) {
		case runtime.SmallInt:
			return l.Value.Cmp(big.NewInt(r.Value)), nil
		case runtime.Int:
			return l.Value.Cmp(r.Value), nil
		case runtime.Decimal:
			return intToDecimal(l).Value.Cmp(r.Value), nil
		}
	case runtime.Decimal:
		switch r := right.(type) {
		case runtime.SmallInt:
			return l.Value.Cmp(new(big.Rat).SetInt64(r.Value)), nil
		case runtime.Int:
			return l.Value.Cmp(intToDecimal(r).Value), nil
		case runtime.Decimal:
			return l.Value.Cmp(r.Value), nil
		}
	case runtime.Float:
		if r, ok := right.(runtime.Float); ok {
			switch {
			case l.Value < r.Value:
				return -1, nil
			case l.Value > r.Value:
				return 1, nil
			default:
				return 0, nil
			}
		}
	case runtime.String:
		if r, ok := right.(runtime.String); ok {
			switch {
			case l.Value < r.Value:
				return -1, nil
			case l.Value > r.Value:
				return 1, nil
			default:
				return 0, nil
			}
		}
	}
	return 0, fmt.Errorf("cannot compare %s and %s", left.TypeName(), right.TypeName())
}

func intToDecimal(value runtime.Int) runtime.Decimal {
	return runtime.Decimal{Value: new(big.Rat).SetInt(value.Value)}
}

func intFloorDivMod(left *big.Int, right *big.Int) (*big.Int, *big.Int) {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(left, right, remainder)
	if remainder.Sign() != 0 && ((left.Sign() < 0) != (right.Sign() < 0)) {
		quotient.Sub(quotient, big.NewInt(1))
		remainder.Add(remainder, right)
	}
	return quotient, remainder
}

func isComparison(operator string) bool {
	return operator == "<" || operator == "<=" || operator == ">" || operator == ">="
}

func dictKey(value runtime.Value) string {
	switch value := value.(type) {
	case runtime.Null:
		return "n"
	case runtime.Bool:
		if value.Value {
			return "b1"
		}
		return "b0"
	case runtime.SmallInt:
		return "i" + strconv.FormatInt(value.Value, 10)
	case runtime.Int:
		return "i" + value.Value.String()
	case runtime.Decimal:
		return "d" + value.Value.RatString()
	case runtime.Float:
		floatValue := value.Value
		if floatValue == 0 {
			floatValue = 0
		}
		return "f" + strconv.FormatFloat(floatValue, 'g', -1, 64)
	case runtime.String:
		// Single-byte type prefix; matches native.DictKey so dicts
		// look up identically whether built by VM or evaluator.
		return "s" + value.Value
	case runtime.Bytes:
		return "y" + hex.EncodeToString(value.Value)
	case *runtime.List:
		parts := make([]string, 0, len(value.Elements))
		for _, element := range value.Elements {
			parts = append(parts, dictKey(element))
		}
		return "L[" + strings.Join(parts, ",") + "]"
	case runtime.Set:
		parts := make([]string, 0, len(value.Elements))
		for key := range value.Elements {
			parts = append(parts, key)
		}
		sort.Strings(parts)
		return "S{" + strings.Join(parts, ",") + "}"
	case runtime.Dict:
		type kv struct{ k, v string }
		pairs := make([]kv, 0, len(value.Entries))
		for k, entry := range value.Entries {
			pairs = append(pairs, kv{k, dictKey(entry.Value)})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
		parts := make([]string, len(pairs))
		for i, p := range pairs {
			parts[i] = p.k + "=" + p.v
		}
		return "D{" + strings.Join(parts, ",") + "}"
	case runtime.Range:
		return "r" + value.Start.String() + ":" + value.End.String() + ":" + value.Step.String() + ":" + strconv.FormatBool(value.Exclusive)
	case runtime.BytecodeFunction:
		return fmt.Sprintf("bytecode-func:%s:%s:%d", value.Module, value.Name, value.Index)
	case runtime.BytecodeClass:
		return fmt.Sprintf("bytecode-class:%s:%s:%d", value.Module, value.Name, value.Index)
	case runtime.NativeObject:
		return fmt.Sprintf("native:%s:%d", value.Kind, value.ID)
	case runtime.Error:
		return "error:" + strconv.Quote(value.Class) + ":" + strconv.Quote(value.Message)
	case runtime.Type:
		return "type:" + strconv.Quote(value.Name)
	case *runtime.Module:
		return fmt.Sprintf("module:%p", value)
	case *runtime.Class:
		return fmt.Sprintf("class:%p", value)
	case *runtime.Interface:
		return fmt.Sprintf("interface:%p", value)
	case *runtime.Instance:
		return fmt.Sprintf("instance:%p", value)
	default:
		return fmt.Sprintf("%T:%p", value, &value)
	}
}

func cloneSetEntries(elements map[string]runtime.SetEntry) map[string]runtime.SetEntry {
	out := make(map[string]runtime.SetEntry, len(elements))
	for key, entry := range elements {
		out[key] = entry
	}
	return out
}

func orderedSetValues(value runtime.Set) []runtime.Value {
	keys := make([]string, 0, len(value.Elements))
	for key := range value.Elements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]runtime.Value, 0, len(keys))
	for _, key := range keys {
		values = append(values, value.Elements[key].Value)
	}
	return values
}

func indexInt(value runtime.Value) (int, error) {
	if small, ok := value.(runtime.SmallInt); ok {
		return int(small.Value), nil
	}
	intValue, ok := value.(runtime.Int)
	if !ok {
		return 0, fmt.Errorf("index must be int, got %s", value.TypeName())
	}
	if !intValue.Value.IsInt64() {
		return 0, fmt.Errorf("index is out of range")
	}
	return int(intValue.Value.Int64()), nil
}

func listElement(value *runtime.List, i int) (runtime.Value, error) {
	if i < 0 {
		i = len(value.Elements) + i
	}
	if i < 0 || i >= len(value.Elements) {
		return nil, fmt.Errorf("list index out of range")
	}
	return value.Elements[i], nil
}

func stringElement(value runtime.String, i int) (runtime.Value, error) {
	runes := []rune(value.Value)
	if i < 0 {
		i = len(runes) + i
	}
	if i < 0 || i >= len(runes) {
		return nil, fmt.Errorf("string index out of range")
	}
	return runtime.String{Value: string(runes[i])}, nil
}

func bytesElement(value runtime.Bytes, i int) (runtime.Value, error) {
	if i < 0 {
		i = len(value.Value) + i
	}
	if i < 0 || i >= len(value.Value) {
		return nil, fmt.Errorf("bytes index out of range")
	}
	return runtime.NewInt64(int64(value.Value[i])), nil
}

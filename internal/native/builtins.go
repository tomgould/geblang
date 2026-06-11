package native

import (
	"bytes"
	"compress/gzip"
	aescipher "crypto/aes"
	ciphermode "crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"html"
	htmltemplate "html/template"
	"io"
	"math"
	"math/big"
	mrand "math/rand"
	nethttp "net/http"
	"net/url"
	"os"
	pathlib "path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"geblang/internal/runtime"

	tomllib "github.com/BurntSushi/toml"
	googleuuid "github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	goldast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	goldparser "github.com/yuin/goldmark/parser"
	goldhtml "github.com/yuin/goldmark/renderer/html"
	goldtext "github.com/yuin/goldmark/text"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/sha3"
	yamllib "gopkg.in/yaml.v3"
)

// monoClockStart anchors time.monotonic() to process start so it reports
// a monotonic (never-decreasing) millisecond counter via the monotonic
// reading embedded in time.Since.
var monoClockStart = time.Now()

// builtinFunctionsOnce ensures the read-only functions map of the
// builtin registry is constructed exactly once per process. Per-VM
// NewBuiltinRegistry calls then share the same map and only pay for
// the small per-VM patches overlay - shaving ~500 string allocs
// per VM creation that the registerX cascade would otherwise repeat.
var (
	builtinFunctionsOnce sync.Once
	builtinFunctions     map[string]Function
)

func ensureBuiltinFunctions() map[string]Function {
	builtinFunctionsOnce.Do(func() {
		r := NewRegistry()
		registerAllBuiltins(r)
		builtinFunctions = r.functions
	})
	return builtinFunctions
}

// NewBuiltinRegistry returns a Registry pre-populated with all pure built-in functions.
// The function map is built once per process and shared across VMs;
// each call still allocates a fresh per-VM patches overlay so tests
// can install mocks without disturbing other VMs.
func NewBuiltinRegistry() *Registry {
	return &Registry{
		functions: ensureBuiltinFunctions(),
		patches:   map[string]Function{},
	}
}

// registerAllBuiltins is the original NewBuiltinRegistry body, lifted
// out so the one-time initialisation in ensureBuiltinFunctions can
// reuse it. Direct callers should not invoke this - go through
// NewBuiltinRegistry.
func registerAllBuiltins(r *Registry) {
	registerMath(r)
	registerVecmath(r)
	registerHnsw(r)
	registerJSON(r)
	registerCSV(r)
	registerXML(r)
	registerTOML(r)
	registerYAML(r)
	registerCrypt(r)
	registerCryptPKI(r)
	registerCryptJWK(r)
	registerDatetime(r)
	registerSecrets(r)
	registerRandom(r)
	registerSecureRandom(r)
	registerMsgpack(r)
	registerUnicode(r)
	registerCron(r)
	registerAsyncSync(r)
	registerAsyncAtomic(r)
	registerAsyncChannel(r)
	registerStore(r)
	registerImage(r)
	registerTime(r)
	registerBytes(r)
	registerString(r)
	registerStringBuilder(r)
	registerCompress(r)
	registerArchive(r)
	registerEncoding(r)
	registerBinary(r)
	registerURL(r)
	registerUUID(r)
	registerRe(r)
	registerRegexCompile(r)
	registerPCRE(r)
	registerMarkdown(r)
	registerTemplate(r)
	registerSys(r)
	registerArgs(r)
	registerErrors(r)
	registerFreeze(r)
	registerClone(r)
	registerProfiler(r)
}

// PureModuleNames returns the function names registered in a pure module, plus a found flag.
func PureModuleNames(module string) ([]string, bool) {
	fns, ok := pureBuiltins[module]
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(fns))
	for name := range fns {
		names = append(names, name)
	}
	return names, true
}

func registerMath(r *Registry) {
	r.Register("math", "abs", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("math.abs expects exactly one argument")
		}
		return NumericAbs(args[0])
	})
	r.Register("math", "min", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("math.min expects at least one argument")
		}
		return NumericBest(args, func(cmp int) bool { return cmp < 0 })
	})
	r.Register("math", "max", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("math.max expects at least one argument")
		}
		return NumericBest(args, func(cmp int) bool { return cmp > 0 })
	})
	r.Register("math", "clamp", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("math.clamp expects value, min, max")
		}
		if cmp, err := NumericCompare(args[1], args[2]); err != nil {
			return nil, err
		} else if cmp > 0 {
			return nil, fmt.Errorf("math.clamp min must be <= max")
		}
		if cmp, err := NumericCompare(args[0], args[1]); err != nil {
			return nil, err
		} else if cmp < 0 {
			return args[1], nil
		}
		if cmp, err := NumericCompare(args[0], args[2]); err != nil {
			return nil, err
		} else if cmp > 0 {
			return args[2], nil
		}
		return args[0], nil
	})
	r.Register("math", "lerp", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("math.lerp expects (a, b, t)")
		}
		rats, floats, err := interpOperands(args, "math.lerp")
		if err != nil {
			return nil, err
		}
		if floats != nil {
			a, b, t := floats[0], floats[1], floats[2]
			return runtime.Float{Value: a + (b-a)*t}, nil
		}
		a, b, t := rats[0], rats[1], rats[2]
		scaled := new(big.Rat).Mul(new(big.Rat).Sub(b, a), t)
		return runtime.Decimal{Value: new(big.Rat).Add(a, scaled)}, nil
	})
	r.Register("math", "remap", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 5 {
			return nil, fmt.Errorf("math.remap expects (x, inLow, inHigh, outLow, outHigh)")
		}
		rats, floats, err := interpOperands(args, "math.remap")
		if err != nil {
			return nil, err
		}
		if floats != nil {
			x, il, ih, ol, oh := floats[0], floats[1], floats[2], floats[3], floats[4]
			if ih == il {
				return nil, fmt.Errorf("math.remap: input range has zero width (inLow == inHigh)")
			}
			return runtime.Float{Value: ol + (x-il)*(oh-ol)/(ih-il)}, nil
		}
		x, il, ih, ol, oh := rats[0], rats[1], rats[2], rats[3], rats[4]
		den := new(big.Rat).Sub(ih, il)
		if den.Sign() == 0 {
			return nil, fmt.Errorf("math.remap: input range has zero width (inLow == inHigh)")
		}
		num := new(big.Rat).Mul(new(big.Rat).Sub(x, il), new(big.Rat).Sub(oh, ol))
		return runtime.Decimal{Value: new(big.Rat).Add(ol, new(big.Rat).Quo(num, den))}, nil
	})
	r.Register("math", "floor", func(args []runtime.Value) (runtime.Value, error) {
		return IntUnaryMath(args, math.Floor, "math.floor")
	})
	r.Register("math", "ceil", func(args []runtime.Value) (runtime.Value, error) {
		return IntUnaryMath(args, math.Ceil, "math.ceil")
	})
	r.Register("math", "round", func(args []runtime.Value) (runtime.Value, error) {
		return IntUnaryMath(args, math.Round, "math.round")
	})
	r.Register("math", "sqrt", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Sqrt, "math.sqrt")
	})
	r.Register("math", "sin", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Sin, "math.sin")
	})
	r.Register("math", "cos", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Cos, "math.cos")
	})
	r.Register("math", "tan", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Tan, "math.tan")
	})
	r.Register("math", "asin", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Asin, "math.asin")
	})
	r.Register("math", "acos", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Acos, "math.acos")
	})
	r.Register("math", "atan", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Atan, "math.atan")
	})
	r.Register("math", "atan2", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("math.atan2 expects exactly two arguments")
		}
		y, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		x, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: math.Atan2(y, x)}, nil
	})
	r.Register("math", "log", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Log, "math.log")
	})
	r.Register("math", "log10", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Log10, "math.log10")
	})
	r.Register("math", "exp", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Exp, "math.exp")
	})
	r.Register("math", "pow", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("math.pow expects exactly two arguments")
		}
		base, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		exponent, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: math.Pow(base, exponent)}, nil
	})
	r.Register("math", "pi", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.pi expects no arguments")
		}
		return runtime.Float{Value: math.Pi}, nil
	})
	r.Register("math", "e", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.e expects no arguments")
		}
		return runtime.Float{Value: math.E}, nil
	})
	r.Register("math", "tau", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.tau expects no arguments")
		}
		return runtime.Float{Value: 2 * math.Pi}, nil
	})
	r.Register("math", "ln2", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.ln2 expects no arguments")
		}
		return runtime.Float{Value: math.Ln2}, nil
	})
	r.Register("math", "ln10", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.ln10 expects no arguments")
		}
		return runtime.Float{Value: math.Log(10)}, nil
	})
	r.Register("math", "sqrt2", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.sqrt2 expects no arguments")
		}
		return runtime.Float{Value: math.Sqrt2}, nil
	})
	r.Register("math", "phi", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.phi expects no arguments")
		}
		return runtime.Float{Value: math.Phi}, nil
	})
	r.Register("math", "maxInt", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.maxInt expects no arguments")
		}
		return runtime.SmallInt{Value: math.MaxInt64}, nil
	})
	r.Register("math", "minInt", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.minInt expects no arguments")
		}
		return runtime.SmallInt{Value: math.MinInt64}, nil
	})
	r.Register("math", "maxFloat", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.maxFloat expects no arguments")
		}
		return runtime.Float{Value: math.MaxFloat64}, nil
	})
	r.Register("math", "minFloat", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.minFloat expects no arguments")
		}
		return runtime.Float{Value: math.SmallestNonzeroFloat64}, nil
	})
	r.Register("math", "epsilon", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.epsilon expects no arguments")
		}
		return runtime.Float{Value: 2.220446049250313e-16}, nil
	})
	r.Register("math", "sqrt2Pi", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.sqrt2Pi expects no arguments")
		}
		return runtime.Float{Value: math.Sqrt(2 * math.Pi)}, nil
	})
	r.Register("math", "log2Pi", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.log2Pi expects no arguments")
		}
		return runtime.Float{Value: math.Log(2 * math.Pi)}, nil
	})
	r.Register("math", "log2", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Log2, "math.log2")
	})
	r.Register("math", "trunc", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Trunc, "math.trunc")
	})
	r.Register("math", "cbrt", func(args []runtime.Value) (runtime.Value, error) {
		return FloatUnaryMath(args, math.Cbrt, "math.cbrt")
	})
	r.Register("math", "sign", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("math.sign expects exactly one argument")
		}
		v, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		switch {
		case v < 0:
			return runtime.NewInt64(-1), nil
		case v > 0:
			return runtime.NewInt64(1), nil
		default:
			return runtime.NewInt64(0), nil
		}
	})
	r.Register("math", "hypot", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("math.hypot expects exactly two arguments")
		}
		a, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		b, err := FloatLike(args[1])
		if err != nil {
			return nil, err
		}
		return runtime.Float{Value: math.Hypot(a, b)}, nil
	})
	r.Register("math", "inf", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.inf expects no arguments")
		}
		return runtime.Float{Value: math.Inf(1)}, nil
	})
	r.Register("math", "nan", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("math.nan expects no arguments")
		}
		return runtime.Float{Value: math.NaN()}, nil
	})
	r.Register("math", "isNaN", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("math.isNaN expects exactly one argument")
		}
		v, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: math.IsNaN(v)}, nil
	})
	r.Register("math", "isInf", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("math.isInf expects exactly one argument")
		}
		v, err := FloatLike(args[0])
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: math.IsInf(v, 0)}, nil
	})
	r.Register("math", "isPrime", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("math.isPrime expects exactly one argument")
		}
		n, ok := IntValueToBigInt(args[0])
		if !ok {
			return nil, fmt.Errorf("math.isPrime: argument must be an integer")
		}
		return runtime.Bool{Value: n.ProbablyPrime(20)}, nil
	})
	r.Register("math", "median", func(args []runtime.Value) (runtime.Value, error) {
		nums, err := mathNumericList(args, "math.median")
		if err != nil {
			return nil, err
		}
		if len(nums) == 0 {
			return nil, fmt.Errorf("math.median: list must not be empty")
		}
		return runtime.Float{Value: mathQuantile(nums, 0.5)}, nil
	})
	r.Register("math", "percentile", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("math.percentile expects (list, p)")
		}
		nums, err := mathNumericListSingle(args[0], "math.percentile")
		if err != nil {
			return nil, err
		}
		if len(nums) == 0 {
			return nil, fmt.Errorf("math.percentile: list must not be empty")
		}
		p, err := FloatLike(args[1])
		if err != nil {
			return nil, fmt.Errorf("math.percentile: p must be numeric: %v", err)
		}
		if p < 0 || p > 100 {
			return nil, fmt.Errorf("math.percentile: p must be in [0, 100]")
		}
		return runtime.Float{Value: mathQuantile(nums, p/100)}, nil
	})
	r.Register("math", "quantile", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("math.quantile expects (list, q)")
		}
		nums, err := mathNumericListSingle(args[0], "math.quantile")
		if err != nil {
			return nil, err
		}
		if len(nums) == 0 {
			return nil, fmt.Errorf("math.quantile: list must not be empty")
		}
		q, err := FloatLike(args[1])
		if err != nil {
			return nil, fmt.Errorf("math.quantile: q must be numeric: %v", err)
		}
		if q < 0 || q > 1 {
			return nil, fmt.Errorf("math.quantile: q must be in [0, 1]")
		}
		return runtime.Float{Value: mathQuantile(nums, q)}, nil
	})
	r.Register("math", "mode", func(args []runtime.Value) (runtime.Value, error) {
		nums, err := mathNumericList(args, "math.mode")
		if err != nil {
			return nil, err
		}
		if len(nums) == 0 {
			return nil, fmt.Errorf("math.mode: list must not be empty")
		}
		// Count occurrences; ties broken by lowest value (deterministic).
		counts := map[float64]int{}
		for _, v := range nums {
			counts[v]++
		}
		best := nums[0]
		bestCount := 0
		for v, c := range counts {
			if c > bestCount || (c == bestCount && v < best) {
				best = v
				bestCount = c
			}
		}
		return runtime.Float{Value: best}, nil
	})
}

func mathNumericList(args []runtime.Value, label string) ([]float64, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects a single list argument", label)
	}
	return mathNumericListSingle(args[0], label)
}

func mathNumericListSingle(v runtime.Value, label string) ([]float64, error) {
	list, ok := v.(*runtime.List)
	if !ok {
		return nil, fmt.Errorf("%s: argument must be a list", label)
	}
	nums := make([]float64, len(list.Elements))
	for i, elem := range list.Elements {
		f, err := FloatLike(elem)
		if err != nil {
			return nil, fmt.Errorf("%s: list element %d: %v", label, i, err)
		}
		nums[i] = f
	}
	return nums, nil
}

// mathQuantile computes the q-quantile (q in [0, 1]) using R's type-7
// linear-interpolation algorithm - the most common default across
// numpy, pandas, R, Excel.
func mathQuantile(nums []float64, q float64) float64 {
	sorted := append([]float64(nil), nums...)
	sort.Float64s(sorted)
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := pos - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}

func registerJSON(r *Registry) {
	r.Register("json", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "json.parse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseJSONText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return value, nil
	})
	r.Register("json", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("json.stringify expects exactly one argument")
		}
		out, err := EncodeJSONValue(args[0])
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: out}, nil
	})
	r.Register("json", "validate", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "json.validate")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: json.Valid([]byte(text))}, nil
	})
	r.Register("json", "tryParse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "json.tryParse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseJSONText(text)
		if parseErr != nil {
			return ParseResult(false, runtime.Null{}, parseErr), nil
		}
		return ParseResult(true, value, nil), nil
	})
	r.Register("json", "validateDetailed", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "json.validateDetailed")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseJSONText(text)
		if parseErr != nil {
			return ValidationResult(false, parseErr), nil
		}
		return ValidationResult(true, nil), nil
	})
	r.Register("json", "parseAs", func(args []runtime.Value) (runtime.Value, error) {
		text, class, err := parseAsArgs(args, "json.parseAs")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseJSONText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return deserializeIntoClass(class, value)
	})
}

func registerXML(r *Registry) {
	r.Register("xml", "validate", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.validate")
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: ValidateXML(text)}, nil
	})
	r.Register("xml", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.parse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseXML(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return value, nil
	})
	r.Register("xml", "tryParse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.tryParse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseXML(text)
		if parseErr != nil {
			return ParseResult(false, runtime.Null{}, parseErr), nil
		}
		return ParseResult(true, value, nil), nil
	})
	r.Register("xml", "parseAs", func(args []runtime.Value) (runtime.Value, error) {
		text, class, err := parseAsArgs(args, "xml.parseAs")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseXML(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return deserializeIntoClass(class, value)
	})
	r.Register("xml", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("xml.stringify expects exactly one argument")
		}
		text, err := StringifyXML(args[0])
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: text}, nil
	})
	r.Register("xml", "validateDetailed", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "xml.validateDetailed")
		if err != nil {
			return nil, err
		}
		parseErr := ValidateXMLDetailed(text)
		if parseErr != nil {
			return ValidationResult(false, parseErr), nil
		}
		return ValidationResult(true, nil), nil
	})
}

func registerTOML(r *Registry) {
	r.Register("toml", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.parse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseTOMLText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return value, nil
	})
	r.Register("toml", "tryParse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.tryParse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseTOMLText(text)
		if parseErr != nil {
			return ParseResult(false, runtime.Null{}, parseErr), nil
		}
		return ParseResult(true, value, nil), nil
	})
	r.Register("toml", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("toml.stringify expects exactly one argument")
		}
		encoded, err := ValueToTOML(args[0])
		if err != nil {
			return nil, err
		}
		top, ok := encoded.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("toml.stringify expects a dict at top level")
		}
		var out bytes.Buffer
		if err := tomllib.NewEncoder(&out).Encode(top); err != nil {
			return nil, err
		}
		return runtime.String{Value: strings.TrimSuffix(out.String(), "\n")}, nil
	})
	r.Register("toml", "parseAs", func(args []runtime.Value) (runtime.Value, error) {
		text, class, err := parseAsArgs(args, "toml.parseAs")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseTOMLText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return deserializeIntoClass(class, value)
	})
	r.Register("toml", "validate", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.validate")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseTOMLText(text)
		return runtime.Bool{Value: parseErr == nil}, nil
	})
	r.Register("toml", "validateDetailed", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "toml.validateDetailed")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseTOMLText(text)
		if parseErr != nil {
			return ValidationResult(false, parseErr), nil
		}
		return ValidationResult(true, nil), nil
	})
}

func registerYAML(r *Registry) {
	r.Register("yaml", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.parse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseYAMLText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return value, nil
	})
	r.Register("yaml", "tryParse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.tryParse")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseYAMLText(text)
		if parseErr != nil {
			return ParseResult(false, runtime.Null{}, parseErr), nil
		}
		return ParseResult(true, value, nil), nil
	})
	r.Register("yaml", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("yaml.stringify expects exactly one argument")
		}
		encoded, err := ValueToYAML(args[0])
		if err != nil {
			return nil, err
		}
		data, err := yamllib.Marshal(encoded)
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: strings.TrimSuffix(string(data), "\n")}, nil
	})
	r.Register("yaml", "parseAs", func(args []runtime.Value) (runtime.Value, error) {
		text, class, err := parseAsArgs(args, "yaml.parseAs")
		if err != nil {
			return nil, err
		}
		value, parseErr := ParseYAMLText(text)
		if parseErr != nil {
			return nil, fmt.Errorf("%s", parseErr.Message)
		}
		return deserializeIntoClass(class, value)
	})
	r.Register("yaml", "validate", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.validate")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseYAMLText(text)
		return runtime.Bool{Value: parseErr == nil}, nil
	})
	r.Register("yaml", "validateDetailed", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "yaml.validateDetailed")
		if err != nil {
			return nil, err
		}
		_, parseErr := ParseYAMLText(text)
		if parseErr != nil {
			return ValidationResult(false, parseErr), nil
		}
		return ValidationResult(true, nil), nil
	})
}

func registerCrypt(r *Registry) {
	r.Register("crypt", "md5", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleHashInput(args, "crypt.md5")
		if err != nil {
			return nil, err
		}
		sum := md5.Sum(data)
		return runtime.String{Value: hex.EncodeToString(sum[:])}, nil
	})
	r.Register("crypt", "sha1", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleHashInput(args, "crypt.sha1")
		if err != nil {
			return nil, err
		}
		sum := sha1.Sum(data)
		return runtime.String{Value: hex.EncodeToString(sum[:])}, nil
	})
	r.Register("crypt", "sha256", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleHashInput(args, "crypt.sha256")
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		return runtime.String{Value: hex.EncodeToString(sum[:])}, nil
	})
	r.Register("crypt", "sha512", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleHashInput(args, "crypt.sha512")
		if err != nil {
			return nil, err
		}
		sum := sha512.Sum512(data)
		return runtime.String{Value: hex.EncodeToString(sum[:])}, nil
	})
	r.Register("crypt", "sha3_256", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleHashInput(args, "crypt.sha3_256")
		if err != nil {
			return nil, err
		}
		sum := sha3.Sum256(data)
		return runtime.String{Value: hex.EncodeToString(sum[:])}, nil
	})
	r.Register("crypt", "blake2b", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleHashInput(args, "crypt.blake2b")
		if err != nil {
			return nil, err
		}
		sum := blake2b.Sum256(data)
		return runtime.String{Value: hex.EncodeToString(sum[:])}, nil
	})
	r.Register("crypt", "crc32", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleHashInput(args, "crypt.crc32")
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(int64(crc32.ChecksumIEEE(data))), nil
	})
	r.Register("crypt", "hmacSha256", func(args []runtime.Value) (runtime.Value, error) {
		key, msg, err := hmacInputs(args, "crypt.hmacSha256")
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(msg)
		return runtime.String{Value: hex.EncodeToString(mac.Sum(nil))}, nil
	})
	r.Register("crypt", "hmacSha256Bytes", func(args []runtime.Value) (runtime.Value, error) {
		key, msg, err := hmacInputs(args, "crypt.hmacSha256Bytes")
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(msg)
		return runtime.Bytes{Value: mac.Sum(nil)}, nil
	})
	r.Register("crypt", "bcryptHash", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("crypt.bcryptHash expects password and optional cost")
		}
		password, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.bcryptHash password must be string")
		}
		cost := bcrypt.DefaultCost
		if len(args) == 2 {
			costVal, ok := AsInt64(args[1])
			if !ok {
				return nil, fmt.Errorf("crypt.bcryptHash cost must be int")
			}
			cost = int(costVal)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password.Value), cost)
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: string(hash)}, nil
	})
	r.Register("crypt", "bcryptVerify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("crypt.bcryptVerify expects password and hash")
		}
		password, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.bcryptVerify password must be string")
		}
		hash, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.bcryptVerify hash must be string")
		}
		err := bcrypt.CompareHashAndPassword([]byte(hash.Value), []byte(password.Value))
		return runtime.Bool{Value: err == nil}, nil
	})
	r.Register("crypt", "argon2idHash", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("crypt.argon2idHash expects password and optional options")
		}
		password, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.argon2idHash password must be string")
		}
		params := defaultArgon2idParams()
		if len(args) == 2 {
			options, ok := args[1].(runtime.Dict)
			if !ok {
				return nil, fmt.Errorf("crypt.argon2idHash options must be dict")
			}
			if err := applyArgon2idOptions(&params, options); err != nil {
				return nil, err
			}
		}
		salt := make([]byte, params.saltLength)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
		hash := argon2.IDKey([]byte(password.Value), salt, params.time, params.memory, params.parallelism, params.keyLength)
		encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
			params.memory,
			params.time,
			params.parallelism,
			base64.RawStdEncoding.EncodeToString(salt),
			base64.RawStdEncoding.EncodeToString(hash),
		)
		return runtime.String{Value: encoded}, nil
	})
	r.Register("crypt", "argon2idVerify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("crypt.argon2idVerify expects password and hash")
		}
		password, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.argon2idVerify password must be string")
		}
		encoded, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.argon2idVerify hash must be string")
		}
		params, salt, expected, err := parseArgon2idHash(encoded.Value)
		if err != nil {
			return runtime.Bool{Value: false}, nil
		}
		actual := argon2.IDKey([]byte(password.Value), salt, params.time, params.memory, params.parallelism, uint32(len(expected)))
		return runtime.Bool{Value: subtle.ConstantTimeCompare(actual, expected) == 1}, nil
	})
	r.Register("crypt", "passwordHash", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("crypt.passwordHash expects password and optional opts")
		}
		password, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.passwordHash password must be string")
		}
		algorithm := "bcrypt"
		var opts runtime.Dict
		var haveOpts bool
		if len(args) == 2 {
			opts, ok = args[1].(runtime.Dict)
			if !ok {
				return nil, fmt.Errorf("crypt.passwordHash opts must be dict")
			}
			haveOpts = true
			if alg := dictString(opts, "algorithm"); alg != "" {
				algorithm = alg
			}
		}
		switch algorithm {
		case "bcrypt", "2y", "PASSWORD_BCRYPT":
			cost := bcrypt.DefaultCost
			if haveOpts {
				if v, ok := dictInt64(opts, "cost"); ok {
					cost = int(v)
				}
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(password.Value), cost)
			if err != nil {
				return nil, err
			}
			out := string(hash)
			if strings.HasPrefix(out, "$2a$") || strings.HasPrefix(out, "$2b$") {
				out = "$2y$" + out[4:]
			}
			return runtime.String{Value: out}, nil
		case "argon2id", "PASSWORD_ARGON2ID":
			params := defaultArgon2idParams()
			if haveOpts {
				if err := applyArgon2idOptions(&params, opts); err != nil {
					return nil, err
				}
			}
			salt := make([]byte, params.saltLength)
			if _, err := rand.Read(salt); err != nil {
				return nil, err
			}
			hash := argon2.IDKey([]byte(password.Value), salt, params.time, params.memory, params.parallelism, params.keyLength)
			return runtime.String{Value: fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
				params.memory, params.time, params.parallelism,
				base64.RawStdEncoding.EncodeToString(salt),
				base64.RawStdEncoding.EncodeToString(hash))}, nil
		case "argon2i", "PASSWORD_ARGON2I":
			params := defaultArgon2idParams()
			if haveOpts {
				if err := applyArgon2idOptions(&params, opts); err != nil {
					return nil, err
				}
			}
			salt := make([]byte, params.saltLength)
			if _, err := rand.Read(salt); err != nil {
				return nil, err
			}
			hash := argon2.Key([]byte(password.Value), salt, params.time, params.memory, params.parallelism, params.keyLength)
			return runtime.String{Value: fmt.Sprintf("$argon2i$v=19$m=%d,t=%d,p=%d$%s$%s",
				params.memory, params.time, params.parallelism,
				base64.RawStdEncoding.EncodeToString(salt),
				base64.RawStdEncoding.EncodeToString(hash))}, nil
		}
		return nil, fmt.Errorf("crypt.passwordHash: unknown algorithm %q (expected bcrypt, argon2id, argon2i)", algorithm)
	})
	r.Register("crypt", "passwordVerify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("crypt.passwordVerify expects password and hash")
		}
		password, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.passwordVerify password must be string")
		}
		encoded, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.passwordVerify hash must be string")
		}
		hash := encoded.Value
		switch {
		case strings.HasPrefix(hash, "$2a$"), strings.HasPrefix(hash, "$2b$"), strings.HasPrefix(hash, "$2y$"):
			err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password.Value))
			return runtime.Bool{Value: err == nil}, nil
		case strings.HasPrefix(hash, "$argon2"):
			params, salt, expected, variant, err := parseArgon2Hash(hash)
			if err != nil {
				return runtime.Bool{Value: false}, nil
			}
			var actual []byte
			switch variant {
			case "argon2id":
				actual = argon2.IDKey([]byte(password.Value), salt, params.time, params.memory, params.parallelism, uint32(len(expected)))
			case "argon2i":
				actual = argon2.Key([]byte(password.Value), salt, params.time, params.memory, params.parallelism, uint32(len(expected)))
			default:
				return runtime.Bool{Value: false}, nil
			}
			return runtime.Bool{Value: subtle.ConstantTimeCompare(actual, expected) == 1}, nil
		}
		return runtime.Bool{Value: false}, nil
	})
	r.Register("crypt", "randomHex", func(args []runtime.Value) (runtime.Value, error) {
		size, err := singleInt64(args, "crypt.randomHex")
		if err != nil {
			return nil, err
		}
		if size < 0 || size > 1<<20 {
			return nil, fmt.Errorf("crypt.randomHex byte count out of range")
		}
		data := make([]byte, size)
		if _, err := rand.Read(data); err != nil {
			return nil, err
		}
		return runtime.String{Value: hex.EncodeToString(data)}, nil
	})
	r.Register("crypt", "base64Encode", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "crypt.base64Encode")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: base64.StdEncoding.EncodeToString([]byte(text))}, nil
	})
	r.Register("crypt", "base64Decode", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "crypt.base64Decode")
		if err != nil {
			return nil, err
		}
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: string(decoded)}, nil
	})
	r.Register("crypt", "jwtSign", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("crypt.jwtSign expects payload, key, and optional opts")
		}
		alg := "HS256"
		kid := ""
		var allowed []string
		if len(args) == 3 {
			a, err := jwtOptsAlg(args[2], "crypt.jwtSign")
			if err != nil {
				return nil, err
			}
			if a != "" {
				alg = a
			}
			al, err := jwtOptsAllowedAlgs(args[2], "crypt.jwtSign")
			if err != nil {
				return nil, err
			}
			allowed = al
			if opts, ok := args[2].(runtime.Dict); ok {
				kid = dictString(opts, "kid")
			}
		}
		return jwtSignWithAlg(args[0], args[1], alg, allowed, kid, "crypt.jwtSign")
	})
	r.Register("crypt", "jwtVerify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, fmt.Errorf("crypt.jwtVerify expects token, key, and optional opts")
		}
		token, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("crypt.jwtVerify token must be string")
		}
		var allowed []string
		if len(args) == 3 {
			a, err := jwtOptsAllowedAlgs(args[2], "crypt.jwtVerify")
			if err != nil {
				return nil, err
			}
			allowed = a
		}
		return jwtVerifyWithAlg(token.Value, args[1], allowed, "crypt.jwtVerify")
	})
	r.Register("crypt", "jwtDecode", func(args []runtime.Value) (runtime.Value, error) {
		token, err := singleString(args, "crypt.jwtDecode")
		if err != nil {
			return nil, err
		}
		parts := strings.SplitN(token, ".", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("crypt.jwtDecode invalid JWT format")
		}
		headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			return nil, fmt.Errorf("crypt.jwtDecode invalid header encoding")
		}
		payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("crypt.jwtDecode invalid payload encoding")
		}
		headerVal, parseErr := ParseJSONText(string(headerBytes))
		if parseErr != nil {
			return nil, fmt.Errorf("crypt.jwtDecode invalid header JSON")
		}
		payloadVal, parseErr := ParseJSONText(string(payloadBytes))
		if parseErr != nil {
			return nil, fmt.Errorf("crypt.jwtDecode invalid payload JSON")
		}
		headerKey := runtime.String{Value: "header"}
		payloadKey := runtime.String{Value: "payload"}
		entries := map[string]runtime.DictEntry{
			DictKey(headerKey):  {Key: headerKey, Value: headerVal},
			DictKey(payloadKey): {Key: payloadKey, Value: payloadVal},
		}
		return runtime.Dict{Entries: entries}, nil
	})
	r.Register("crypt", "aesEncrypt", aesEncryptFn)
	r.Register("crypt", "aesDecrypt", aesDecryptFn)
	r.Register("crypt", "chacha20Encrypt", chacha20EncryptFn)
	r.Register("crypt", "chacha20Decrypt", chacha20DecryptFn)
}

// aeadBytesArg extracts a byte slice from a runtime String or Bytes value.
// AES/ChaCha20 callers can pass either, since cipher keys / nonces are often
// generated by other crypt functions that return strings.
func aeadBytesArg(v runtime.Value, name string) ([]byte, error) {
	switch x := v.(type) {
	case runtime.Bytes:
		return x.Value, nil
	case runtime.String:
		return []byte(x.Value), nil
	default:
		return nil, fmt.Errorf("%s must be bytes or string", name)
	}
}

// aeadResultDict packs an AEAD output into {"nonce": Bytes, "ciphertext": Bytes}.
func aeadResultDict(nonce, ciphertext []byte) runtime.Dict {
	nonceKey := runtime.String{Value: "nonce"}
	ctKey := runtime.String{Value: "ciphertext"}
	entries := map[string]runtime.DictEntry{
		DictKey(nonceKey): {Key: nonceKey, Value: runtime.Bytes{Value: nonce}},
		DictKey(ctKey):    {Key: ctKey, Value: runtime.Bytes{Value: ciphertext}},
	}
	return runtime.Dict{Entries: entries}
}

// aeadOptionalAAD returns the AAD bytes from args[start] if present, else nil.
func aeadOptionalAAD(args []runtime.Value, start int, name string) ([]byte, error) {
	if len(args) <= start {
		return nil, nil
	}
	if _, ok := args[start].(runtime.Null); ok {
		return nil, nil
	}
	return aeadBytesArg(args[start], name+" associated data")
}

func aesEncryptFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("crypt.aesEncrypt expects (key, plaintext, [associatedData])")
	}
	key, err := aeadBytesArg(args[0], "crypt.aesEncrypt key")
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypt.aesEncrypt requires a 32-byte AES-256 key (got %d bytes)", len(key))
	}
	plaintext, err := aeadBytesArg(args[1], "crypt.aesEncrypt plaintext")
	if err != nil {
		return nil, err
	}
	aad, err := aeadOptionalAAD(args, 2, "crypt.aesEncrypt")
	if err != nil {
		return nil, err
	}
	block, err := aescipher.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypt.aesEncrypt: %w", err)
	}
	gcm, err := ciphermode.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypt.aesEncrypt: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypt.aesEncrypt nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)
	return aeadResultDict(nonce, ciphertext), nil
}

func aesDecryptFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 3 || len(args) > 4 {
		return nil, fmt.Errorf("crypt.aesDecrypt expects (key, nonce, ciphertext, [associatedData])")
	}
	key, err := aeadBytesArg(args[0], "crypt.aesDecrypt key")
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypt.aesDecrypt requires a 32-byte AES-256 key (got %d bytes)", len(key))
	}
	nonce, err := aeadBytesArg(args[1], "crypt.aesDecrypt nonce")
	if err != nil {
		return nil, err
	}
	ciphertext, err := aeadBytesArg(args[2], "crypt.aesDecrypt ciphertext")
	if err != nil {
		return nil, err
	}
	aad, err := aeadOptionalAAD(args, 3, "crypt.aesDecrypt")
	if err != nil {
		return nil, err
	}
	block, err := aescipher.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypt.aesDecrypt: %w", err)
	}
	gcm, err := ciphermode.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypt.aesDecrypt: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("crypt.aesDecrypt nonce must be %d bytes", gcm.NonceSize())
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("crypt.aesDecrypt: authentication failed")
	}
	return runtime.Bytes{Value: plaintext}, nil
}

func chacha20EncryptFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("crypt.chacha20Encrypt expects (key, plaintext, [associatedData])")
	}
	key, err := aeadBytesArg(args[0], "crypt.chacha20Encrypt key")
	if err != nil {
		return nil, err
	}
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("crypt.chacha20Encrypt requires a %d-byte key (got %d bytes)", chacha20poly1305.KeySize, len(key))
	}
	plaintext, err := aeadBytesArg(args[1], "crypt.chacha20Encrypt plaintext")
	if err != nil {
		return nil, err
	}
	aad, err := aeadOptionalAAD(args, 2, "crypt.chacha20Encrypt")
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("crypt.chacha20Encrypt: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypt.chacha20Encrypt nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	return aeadResultDict(nonce, ciphertext), nil
}

func chacha20DecryptFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 3 || len(args) > 4 {
		return nil, fmt.Errorf("crypt.chacha20Decrypt expects (key, nonce, ciphertext, [associatedData])")
	}
	key, err := aeadBytesArg(args[0], "crypt.chacha20Decrypt key")
	if err != nil {
		return nil, err
	}
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("crypt.chacha20Decrypt requires a %d-byte key (got %d bytes)", chacha20poly1305.KeySize, len(key))
	}
	nonce, err := aeadBytesArg(args[1], "crypt.chacha20Decrypt nonce")
	if err != nil {
		return nil, err
	}
	ciphertext, err := aeadBytesArg(args[2], "crypt.chacha20Decrypt ciphertext")
	if err != nil {
		return nil, err
	}
	aad, err := aeadOptionalAAD(args, 3, "crypt.chacha20Decrypt")
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("crypt.chacha20Decrypt: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("crypt.chacha20Decrypt nonce must be %d bytes", aead.NonceSize())
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("crypt.chacha20Decrypt: authentication failed")
	}
	return runtime.Bytes{Value: plaintext}, nil
}

func registerDatetime(r *Registry) {
	r.Register("datetime", "nowUnix", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.nowUnix expects no arguments")
		}
		return runtime.NewInt64(time.Now().Unix()), nil
	})
	r.Register("datetime", "unix", func(args []runtime.Value) (runtime.Value, error) {
		seconds, err := singleInt64(args, "datetime.unix")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: time.Unix(seconds, 0).UTC().Format(time.RFC3339)}, nil
	})
	r.Register("datetime", "parse", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("datetime.parse expects text and an optional layout")
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("datetime.parse text must be string")
		}
		layout := time.RFC3339
		if len(args) == 2 {
			layoutArg, ok := args[1].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("datetime.parse layout must be string")
			}
			resolved, err := ResolveDateLayout(layoutArg.Value)
			if err != nil {
				return nil, fmt.Errorf("datetime.parse: %v", err)
			}
			layout = resolved
		}
		parsed, err := time.Parse(layout, text.Value)
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(parsed.Unix()), nil
	})
	r.Register("datetime", "format", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("datetime.format expects exactly two arguments")
		}
		sec, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("datetime.format unix seconds must be int")
		}
		layout, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("datetime.format layout must be string")
		}
		goLayout, err := ResolveDateLayout(layout.Value)
		if err != nil {
			return nil, fmt.Errorf("datetime.format: %v", err)
		}
		return runtime.String{Value: time.Unix(sec, 0).UTC().Format(goLayout)}, nil
	})
	r.Register("datetime", "addSeconds", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("datetime.addSeconds expects exactly two arguments")
		}
		seconds, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("datetime.addSeconds unix seconds must be int")
		}
		delta, ok := AsInt64(args[1])
		if !ok {
			return nil, fmt.Errorf("datetime.addSeconds delta must be int")
		}
		return runtime.NewInt64(seconds + delta), nil
	})
	r.Register("datetime", "addDays", func(args []runtime.Value) (runtime.Value, error) {
		seconds, days, err := twoInt64(args, "datetime.addDays")
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(time.Unix(seconds, 0).UTC().AddDate(0, 0, int(days)).Unix()), nil
	})
	r.Register("datetime", "addMonths", func(args []runtime.Value) (runtime.Value, error) {
		seconds, months, err := twoInt64(args, "datetime.addMonths")
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(time.Unix(seconds, 0).UTC().AddDate(0, int(months), 0).Unix()), nil
	})
	r.Register("datetime", "addYears", func(args []runtime.Value) (runtime.Value, error) {
		seconds, years, err := twoInt64(args, "datetime.addYears")
		if err != nil {
			return nil, err
		}
		return runtime.NewInt64(time.Unix(seconds, 0).UTC().AddDate(int(years), 0, 0).Unix()), nil
	})
	r.Register("datetime", "diff", func(args []runtime.Value) (runtime.Value, error) {
		start, end, err := twoInt64(args, "datetime.diff")
		if err != nil {
			return nil, err
		}
		delta := end - start
		if delta < 0 {
			delta = -delta
		}
		days := delta / 86400
		delta %= 86400
		hours := delta / 3600
		delta %= 3600
		minutes := delta / 60
		seconds := delta % 60
		return stringIntDict(map[string]int64{
			"days":    days,
			"hours":   hours,
			"minutes": minutes,
			"seconds": seconds,
		}), nil
	})
	r.Register("datetime", "toLocal", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("datetime.toLocal expects unix seconds and timezone")
		}
		sec, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("datetime.toLocal unix seconds must be int")
		}
		tz, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("datetime.toLocal timezone must be string")
		}
		location, err := time.LoadLocation(tz.Value)
		if err != nil {
			return nil, fmt.Errorf("datetime.toLocal: %v", err)
		}
		return runtime.String{Value: time.Unix(sec, 0).In(location).Format(time.RFC3339)}, nil
	})
	r.Register("datetime", "toUtc", func(args []runtime.Value) (runtime.Value, error) {
		seconds, err := singleInt64(args, "datetime.toUtc")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: time.Unix(seconds, 0).UTC().Format(time.RFC3339)}, nil
	})
	r.Register("datetime", "now", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) > 1 {
			return nil, fmt.Errorf("datetime.now expects an optional timezone name")
		}
		if len(args) == 1 {
			tz, ok := args[0].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("datetime.now timezone must be string")
			}
			loc, err := time.LoadLocation(tz.Value)
			if err != nil {
				return nil, fmt.Errorf("datetime.now: %v", err)
			}
			return timePartsDictWithZone(time.Now().In(loc), tz.Value), nil
		}
		return timePartsDictWithZone(time.Now().UTC(), "UTC"), nil
	})
	r.Register("datetime", "partsInZone", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("datetime.partsInZone expects unix seconds and timezone")
		}
		sec, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("datetime.partsInZone unix seconds must be int")
		}
		tz, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("datetime.partsInZone timezone must be string")
		}
		loc, err := time.LoadLocation(tz.Value)
		if err != nil {
			return nil, fmt.Errorf("datetime.partsInZone: %v", err)
		}
		return timePartsDictWithZone(time.Unix(sec, 0).In(loc), tz.Value), nil
	})
	r.Register("datetime", "formatHTTP", func(args []runtime.Value) (runtime.Value, error) {
		seconds, err := singleInt64(args, "datetime.formatHTTP")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: time.Unix(seconds, 0).UTC().Format(nethttp.TimeFormat)}, nil
	})
	r.Register("datetime", "nowInstant", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.nowInstant expects no arguments")
		}
		return runtime.DateTimeInstant{Unix: time.Now().Unix()}, nil
	})
	r.Register("datetime", "Instant", func(args []runtime.Value) (runtime.Value, error) {
		switch len(args) {
		case 0:
			return runtime.DateTimeInstant{Unix: time.Now().Unix()}, nil
		case 1:
			switch value := args[0].(type) {
			case runtime.DateTimeInstant:
				return value, nil // value type: returning it is the copy
			case runtime.SmallInt:
				return runtime.DateTimeInstant{Unix: value.Value}, nil
			case runtime.Int:
				if !value.Value.IsInt64() {
					return nil, fmt.Errorf("datetime.Instant unix seconds must fit int64")
				}
				return runtime.DateTimeInstant{Unix: value.Value.Int64()}, nil
			case runtime.String:
				parsed, err := time.Parse(time.RFC3339, value.Value)
				if err != nil {
					return nil, fmt.Errorf("datetime.Instant: %v", err)
				}
				return runtime.DateTimeInstant{Unix: parsed.Unix()}, nil
			default:
				return nil, fmt.Errorf("datetime.Instant expects int, string, or datetime.Instant")
			}
		case 3, 4, 5, 6:
			ints := make([]int64, 6)
			for i, a := range args {
				v, ok := AsInt64(a)
				if !ok {
					return nil, fmt.Errorf("datetime.Instant calendar arguments must be int")
				}
				ints[i] = v
			}
			t := time.Date(int(ints[0]), time.Month(ints[1]), int(ints[2]), int(ints[3]), int(ints[4]), int(ints[5]), 0, time.UTC)
			return runtime.DateTimeInstant{Unix: t.Unix()}, nil
		default:
			return nil, fmt.Errorf("datetime.Instant expects 0 args (now), 1 (unix/string/Instant), or 3-6 (year, month, day[, hour, minute, second])")
		}
	})
	r.Register("datetime", "Duration", func(args []runtime.Value) (runtime.Value, error) {
		seconds, err := singleInt64(args, "datetime.Duration")
		if err != nil {
			return nil, err
		}
		return runtime.DateTimeDuration{Seconds: seconds}, nil
	})
	r.Register("datetime", "Zone", func(args []runtime.Value) (runtime.Value, error) {
		name, err := singleString(args, "datetime.Zone")
		if err != nil {
			return nil, err
		}
		if _, err := time.LoadLocation(name); err != nil {
			return nil, fmt.Errorf("datetime.Zone: %v", err)
		}
		return runtime.DateTimeZone{Name: name}, nil
	})
	r.Register("datetime", "sleep", func(args []runtime.Value) (runtime.Value, error) {
		ms, err := singleInt64(args, "datetime.sleep")
		if err != nil {
			return nil, err
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return runtime.Null{}, nil
	})
	r.Register("datetime", "make", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 3 || len(args) > 6 {
			return nil, fmt.Errorf("datetime.make expects 3 to 6 arguments (year, month, day[, hour, minute, second])")
		}
		ints := make([]int64, 6)
		for i, a := range args {
			v, ok := AsInt64(a)
			if !ok {
				return nil, fmt.Errorf("datetime.make arguments must be int")
			}
			ints[i] = v
		}
		t := time.Date(int(ints[0]), time.Month(ints[1]), int(ints[2]), int(ints[3]), int(ints[4]), int(ints[5]), 0, time.UTC)
		return runtime.NewInt64(t.Unix()), nil
	})
	r.Register("datetime", "formatRFC3339", func(args []runtime.Value) (runtime.Value, error) {
		seconds, err := singleInt64(args, "datetime.formatRFC3339")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: time.Unix(seconds, 0).UTC().Format(time.RFC3339)}, nil
	})
	r.Register("datetime", "formatDate", func(args []runtime.Value) (runtime.Value, error) {
		seconds, err := singleInt64(args, "datetime.formatDate")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: time.Unix(seconds, 0).UTC().Format("2006-01-02")}, nil
	})
	r.Register("datetime", "formatTime", func(args []runtime.Value) (runtime.Value, error) {
		seconds, err := singleInt64(args, "datetime.formatTime")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: time.Unix(seconds, 0).UTC().Format("15:04:05")}, nil
	})
	r.Register("datetime", "parseRFC3339", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "datetime.parseRFC3339")
		if err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return nil, fmt.Errorf("datetime.parseRFC3339: %v", err)
		}
		return runtime.NewInt64(parsed.Unix()), nil
	})
	r.Register("datetime", "weekdayName", func(args []runtime.Value) (runtime.Value, error) {
		n, err := singleInt64(args, "datetime.weekdayName")
		if err != nil {
			return nil, err
		}
		names := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		idx := int(n) % 7
		if idx < 0 {
			idx += 7
		}
		return runtime.String{Value: names[idx]}, nil
	})
	r.Register("datetime", "monthName", func(args []runtime.Value) (runtime.Value, error) {
		n, err := singleInt64(args, "datetime.monthName")
		if err != nil {
			return nil, err
		}
		if n < 1 || n > 12 {
			return nil, fmt.Errorf("datetime.monthName month must be between 1 and 12")
		}
		return runtime.String{Value: time.Month(n).String()}, nil
	})
}

func DateTimeInstantMethod(receiver runtime.DateTimeInstant, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "copy":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.copy expects no arguments")
		}
		return receiver, nil // value type: the returned value is the copy
	case "unix", "toUnix":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.%s expects no arguments", name)
		}
		return runtime.NewInt64(receiver.Unix), nil
	case "toUnixMillis":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.toUnixMillis expects no arguments")
		}
		return runtime.NewInt64(receiver.Unix * 1000), nil
	case "toUnixNanos":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.toUnixNanos expects no arguments")
		}
		return runtime.NewInt64(receiver.Unix * 1_000_000_000), nil
	case "toString", "formatRFC3339", "toUtc":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.%s expects no arguments", name)
		}
		return runtime.String{Value: time.Unix(receiver.Unix, 0).UTC().Format(time.RFC3339)}, nil
	case "formatHTTP":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.formatHTTP expects no arguments")
		}
		return runtime.String{Value: time.Unix(receiver.Unix, 0).UTC().Format(nethttp.TimeFormat)}, nil
	case "format":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Instant.format expects layout")
		}
		layout, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("datetime.Instant.format layout must be string")
		}
		goLayout, err := ResolveDateLayout(layout.Value)
		if err != nil {
			return nil, fmt.Errorf("datetime.Instant.format: %v", err)
		}
		return runtime.String{Value: time.Unix(receiver.Unix, 0).UTC().Format(goLayout)}, nil
	case "toLocal":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Instant.toLocal expects timezone")
		}
		location, err := datetimeLocation(args[0])
		if err != nil {
			return nil, fmt.Errorf("datetime.Instant.toLocal: %v", err)
		}
		return runtime.String{Value: time.Unix(receiver.Unix, 0).In(location).Format(time.RFC3339)}, nil
	case "add":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Instant.add expects datetime.Duration")
		}
		duration, ok := args[0].(runtime.DateTimeDuration)
		if !ok {
			return nil, fmt.Errorf("datetime.Instant.add expects datetime.Duration")
		}
		return runtime.DateTimeInstant{Unix: receiver.Unix + duration.Seconds}, nil
	case "addSeconds":
		seconds, err := singleInt64(args, "datetime.Instant.addSeconds")
		if err != nil {
			return nil, err
		}
		return runtime.DateTimeInstant{Unix: receiver.Unix + seconds}, nil
	case "addDays":
		days, err := singleInt64(args, "datetime.Instant.addDays")
		if err != nil {
			return nil, err
		}
		return runtime.DateTimeInstant{Unix: time.Unix(receiver.Unix, 0).UTC().AddDate(0, 0, int(days)).Unix()}, nil
	case "addMonths":
		months, err := singleInt64(args, "datetime.Instant.addMonths")
		if err != nil {
			return nil, err
		}
		return runtime.DateTimeInstant{Unix: time.Unix(receiver.Unix, 0).UTC().AddDate(0, int(months), 0).Unix()}, nil
	case "addYears":
		years, err := singleInt64(args, "datetime.Instant.addYears")
		if err != nil {
			return nil, err
		}
		return runtime.DateTimeInstant{Unix: time.Unix(receiver.Unix, 0).UTC().AddDate(int(years), 0, 0).Unix()}, nil
	case "diff":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Instant.diff expects datetime.Instant")
		}
		other, ok := args[0].(runtime.DateTimeInstant)
		if !ok {
			return nil, fmt.Errorf("datetime.Instant.diff expects datetime.Instant")
		}
		delta := other.Unix - receiver.Unix
		if delta < 0 {
			delta = -delta
		}
		return runtime.DateTimeDuration{Seconds: delta}, nil
	case "sub":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Instant.sub expects datetime.Duration")
		}
		duration, ok := args[0].(runtime.DateTimeDuration)
		if !ok {
			return nil, fmt.Errorf("datetime.Instant.sub expects datetime.Duration")
		}
		return runtime.DateTimeInstant{Unix: receiver.Unix - duration.Seconds}, nil
	case "isBefore", "isAfter", "equals":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Instant.%s expects datetime.Instant", name)
		}
		other, ok := args[0].(runtime.DateTimeInstant)
		if !ok {
			return nil, fmt.Errorf("datetime.Instant.%s expects datetime.Instant", name)
		}
		switch name {
		case "isBefore":
			return runtime.Bool{Value: receiver.Unix < other.Unix}, nil
		case "isAfter":
			return runtime.Bool{Value: receiver.Unix > other.Unix}, nil
		default:
			return runtime.Bool{Value: receiver.Unix == other.Unix}, nil
		}
	case "inZone":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Instant.inZone expects a zone")
		}
		location, err := datetimeLocation(args[0])
		if err != nil {
			return nil, fmt.Errorf("datetime.Instant.inZone: %v", err)
		}
		return timePartsDictWithZone(time.Unix(receiver.Unix, 0).In(location), location.String()), nil
	case "parts":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.parts expects no arguments")
		}
		return timePartsDict(time.Unix(receiver.Unix, 0).UTC()), nil
	case "year", "month", "day", "hour", "minute", "second", "weekday", "dayOfYear", "isWeekend":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Instant.%s expects no arguments", name)
		}
		t := time.Unix(receiver.Unix, 0).UTC()
		switch name {
		case "year":
			return runtime.NewInt64(int64(t.Year())), nil
		case "month":
			return runtime.NewInt64(int64(t.Month())), nil
		case "day":
			return runtime.NewInt64(int64(t.Day())), nil
		case "hour":
			return runtime.NewInt64(int64(t.Hour())), nil
		case "minute":
			return runtime.NewInt64(int64(t.Minute())), nil
		case "second":
			return runtime.NewInt64(int64(t.Second())), nil
		case "weekday":
			return runtime.NewInt64(int64(isoWeekday(t))), nil
		case "dayOfYear":
			return runtime.NewInt64(int64(t.YearDay())), nil
		default:
			wd := t.Weekday()
			return runtime.Bool{Value: wd == time.Saturday || wd == time.Sunday}, nil
		}
	default:
		return nil, fmt.Errorf("datetime.Instant has no method %s", name)
	}
}

func DateTimeDurationMethod(receiver runtime.DateTimeDuration, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "seconds", "inSeconds":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Duration.%s expects no arguments", name)
		}
		return runtime.NewInt64(receiver.Seconds), nil
	case "inMillis":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Duration.inMillis expects no arguments")
		}
		return runtime.NewInt64(receiver.Seconds * 1000), nil
	case "inNanos":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Duration.inNanos expects no arguments")
		}
		return runtime.NewInt64(receiver.Seconds * 1_000_000_000), nil
	case "abs":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Duration.abs expects no arguments")
		}
		s := receiver.Seconds
		if s < 0 {
			s = -s
		}
		return runtime.DateTimeDuration{Seconds: s}, nil
	case "negate":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Duration.negate expects no arguments")
		}
		return runtime.DateTimeDuration{Seconds: -receiver.Seconds}, nil
	case "add", "sub":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Duration.%s expects datetime.Duration", name)
		}
		other, ok := args[0].(runtime.DateTimeDuration)
		if !ok {
			return nil, fmt.Errorf("datetime.Duration.%s expects datetime.Duration", name)
		}
		if name == "add" {
			return runtime.DateTimeDuration{Seconds: receiver.Seconds + other.Seconds}, nil
		}
		return runtime.DateTimeDuration{Seconds: receiver.Seconds - other.Seconds}, nil
	case "toDict":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Duration.toDict expects no arguments")
		}
		return durationPartsDict(receiver.Seconds), nil
	case "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Duration.toString expects no arguments")
		}
		return runtime.String{Value: fmt.Sprintf("%ds", receiver.Seconds)}, nil
	default:
		return nil, fmt.Errorf("datetime.Duration has no method %s", name)
	}
}

func DateTimeZoneMethod(receiver runtime.DateTimeZone, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "name", "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Zone.%s expects no arguments", name)
		}
		return runtime.String{Value: receiver.Name}, nil
	case "offset":
		if len(args) != 0 {
			return nil, fmt.Errorf("datetime.Zone.offset expects no arguments")
		}
		location, err := time.LoadLocation(receiver.Name)
		if err != nil {
			return nil, fmt.Errorf("datetime.Zone.offset: %v", err)
		}
		_, offset := time.Now().In(location).Zone()
		return runtime.NewInt64(int64(offset)), nil
	case "offsetAt":
		if len(args) != 1 {
			return nil, fmt.Errorf("datetime.Zone.offsetAt expects datetime.Instant")
		}
		instant, ok := args[0].(runtime.DateTimeInstant)
		if !ok {
			return nil, fmt.Errorf("datetime.Zone.offsetAt expects datetime.Instant")
		}
		location, err := time.LoadLocation(receiver.Name)
		if err != nil {
			return nil, fmt.Errorf("datetime.Zone.offsetAt: %v", err)
		}
		_, offset := time.Unix(instant.Unix, 0).In(location).Zone()
		return runtime.NewInt64(int64(offset)), nil
	default:
		return nil, fmt.Errorf("datetime.Zone has no method %s", name)
	}
}

func datetimeLocation(value runtime.Value) (*time.Location, error) {
	switch value := value.(type) {
	case runtime.String:
		return time.LoadLocation(value.Value)
	case runtime.DateTimeZone:
		return time.LoadLocation(value.Name)
	default:
		return nil, fmt.Errorf("timezone must be string or datetime.Zone")
	}
}

func durationPartsDict(seconds int64) runtime.Value {
	if seconds < 0 {
		seconds = -seconds
	}
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	minutes := seconds / 60
	seconds %= 60
	return stringIntDict(map[string]int64{
		"days":    days,
		"hours":   hours,
		"minutes": minutes,
		"seconds": seconds,
	})
}

func twoInt64(args []runtime.Value, label string) (int64, int64, error) {
	if len(args) != 2 {
		return 0, 0, fmt.Errorf("%s expects exactly two integer arguments", label)
	}
	l, ok := AsInt64(args[0])
	if !ok {
		return 0, 0, fmt.Errorf("%s first argument must be int", label)
	}
	r, ok := AsInt64(args[1])
	if !ok {
		return 0, 0, fmt.Errorf("%s second argument must be int", label)
	}
	return l, r, nil
}

func stringIntDict(values map[string]int64) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	for key, value := range values {
		keyValue := runtime.String{Value: key}
		entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: runtime.NewInt64(value)}
	}
	return runtime.Dict{Entries: entries}
}

func timePartsDict(value time.Time) runtime.Dict {
	return stringIntDict(map[string]int64{
		"timestamp": int64(value.Unix()),
		"year":      int64(value.Year()),
		"month":     int64(value.Month()),
		"day":       int64(value.Day()),
		"hour":      int64(value.Hour()),
		"minute":    int64(value.Minute()),
		"second":    int64(value.Second()),
		"weekday":   int64(value.Weekday()),
	})
}

// timePartsDictWithZone is like timePartsDict but also records the zone
// name string so source-level wrappers can preserve it without a second
// lookup.
func timePartsDictWithZone(value time.Time, zone string) runtime.Dict {
	intParts := map[string]int64{
		"timestamp": int64(value.Unix()),
		"year":      int64(value.Year()),
		"month":     int64(value.Month()),
		"day":       int64(value.Day()),
		"hour":      int64(value.Hour()),
		"minute":    int64(value.Minute()),
		"second":    int64(value.Second()),
		"weekday":   int64(value.Weekday()),
	}
	entries := map[string]runtime.DictEntry{}
	for k, v := range intParts {
		key := runtime.String{Value: k}
		entries[DictKey(key)] = runtime.DictEntry{Key: key, Value: runtime.NewInt64(v)}
	}
	zoneKey := runtime.String{Value: "zone"}
	entries[DictKey(zoneKey)] = runtime.DictEntry{Key: zoneKey, Value: runtime.String{Value: zone}}
	return runtime.Dict{Entries: entries}
}

func registerSecrets(r *Registry) {
	r.Register("secrets", "randomBytes", func(args []runtime.Value) (runtime.Value, error) {
		data, err := secureRandomBytes(args, "secrets.randomBytes")
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: data}, nil
	})
	r.Register("secrets", "randomInt", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("secrets.randomInt expects min and max")
		}
		minBig, ok := IntValueToBigInt(args[0])
		if !ok {
			return nil, fmt.Errorf("secrets.randomInt min must be int")
		}
		maxBig, ok := IntValueToBigInt(args[1])
		if !ok {
			return nil, fmt.Errorf("secrets.randomInt max must be int")
		}
		if minBig.Cmp(maxBig) > 0 {
			return nil, fmt.Errorf("secrets.randomInt min must be <= max")
		}
		width := new(big.Int).Sub(maxBig, minBig)
		width.Add(width, big.NewInt(1))
		offset, err := rand.Int(rand.Reader, width)
		if err != nil {
			return nil, err
		}
		result := offset.Add(offset, minBig)
		if result.IsInt64() {
			return runtime.SmallInt{Value: result.Int64()}, nil
		}
		return runtime.Int{Value: result}, nil
	})
	r.Register("secrets", "randomHex", func(args []runtime.Value) (runtime.Value, error) {
		data, err := secureRandomBytes(args, "secrets.randomHex")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: hex.EncodeToString(data)}, nil
	})
	r.Register("secrets", "randomBase64", func(args []runtime.Value) (runtime.Value, error) {
		data, err := secureRandomBytes(args, "secrets.randomBase64")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: base64.RawURLEncoding.EncodeToString(data)}, nil
	})
	r.Register("secrets", "constantTimeEqual", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("secrets.constantTimeEqual expects exactly two arguments")
		}
		left, err := secretComparableBytes(args[0])
		if err != nil {
			return nil, err
		}
		right, err := secretComparableBytes(args[1])
		if err != nil {
			return nil, err
		}
		return runtime.Bool{Value: constantTimeEqual(left, right)}, nil
	})
}

// randomGenerators tracks per-generator *mrand.Rand instances keyed by
// the int64 handle stored in the NativeObject returned by
// random.Generator(seed). A sync.Mutex protects concurrent access.
var (
	randomGeneratorMu sync.Mutex
	randomGenerators  = map[int64]*mrand.Rand{}
	randomNextID      int64
)

// registerTime exposes monotonic-style timing primitives distinct from
// the calendar/zone-aware datetime module. Use time for measuring
// elapsed durations, throttling, debouncing, and blocking sleeps.
func registerTime(r *Registry) {
	r.Register("time", "now", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.now expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixMilli()), nil
	})
	r.Register("time", "elapsed", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("time.elapsed expects one argument (start time in ms)")
		}
		start, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("time.elapsed start must be int")
		}
		return runtime.NewInt64(time.Now().UnixMilli() - start), nil
	})
	r.Register("time", "sleep", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("time.sleep expects one argument (milliseconds)")
		}
		ms, ok := AsInt64(args[0])
		if !ok || ms < 0 {
			return nil, fmt.Errorf("time.sleep milliseconds must be a non-negative int")
		}
		if ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		return runtime.Null{}, nil
	})
	r.Register("time", "unix", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unix expects no arguments")
		}
		return runtime.NewInt64(time.Now().Unix()), nil
	})
	r.Register("time", "monotonic", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.monotonic expects no arguments")
		}
		// Monotonic milliseconds since process start. Unlike time.now /
		// time.unix (wall clock, which can jump backwards on NTP / VM
		// clock correction), this never decreases, so it is the correct
		// source for measuring durations, timeouts, and TTLs.
		return runtime.NewInt64(time.Since(monoClockStart).Milliseconds()), nil
	})
	r.Register("time", "monotonicNs", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.monotonicNs expects no arguments")
		}
		return runtime.NewInt64(time.Since(monoClockStart).Nanoseconds()), nil
	})
	r.Register("time", "unixMilli", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixMilli expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixMilli()), nil
	})
	r.Register("time", "unixMicro", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixMicro expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixMicro()), nil
	})
	r.Register("time", "unixNano", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixNano expects no arguments")
		}
		return runtime.NewInt64(time.Now().UnixNano()), nil
	})
	r.Register("time", "unixFloat", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixFloat expects no arguments")
		}
		now := time.Now()
		return runtime.Float{Value: float64(now.Unix()) + float64(now.Nanosecond())/1e9}, nil
	})
	r.Register("time", "unixDecimal", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("time.unixDecimal expects no arguments")
		}
		now := time.Now()
		num := new(big.Int).Mul(big.NewInt(now.Unix()), big.NewInt(1_000_000_000))
		num.Add(num, big.NewInt(int64(now.Nanosecond())))
		return runtime.Decimal{Value: new(big.Rat).SetFrac(num, big.NewInt(1_000_000_000))}, nil
	})
	r.Register("time", "elapsedFloat", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("time.elapsedFloat expects one argument (start time in seconds)")
		}
		start, ok := asFloat64Strict(args[0])
		if !ok {
			return nil, fmt.Errorf("time.elapsedFloat start must be a number")
		}
		now := time.Now()
		current := float64(now.Unix()) + float64(now.Nanosecond())/1e9
		return runtime.Float{Value: current - start}, nil
	})
	r.Register("time", "humanize", func(args []runtime.Value) (runtime.Value, error) {
		ms, ok := singleInt64Arg(args, "time.humanize")
		if !ok {
			return nil, fmt.Errorf("time.humanize expects one int argument (milliseconds)")
		}
		return runtime.String{Value: humanizeMillis(ms)}, nil
	})
}

func singleInt64Arg(args []runtime.Value, label string) (int64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	return AsInt64(args[0])
}

// humanizeMillis renders a millisecond duration as a compact 1-2 unit string
// (e.g. "45ms", "1.5s", "3m 4s", "2h 5m", "1d 1h"). Integer math throughout
// so output is deterministic across backends.
func humanizeMillis(ms int64) string {
	sign := ""
	if ms < 0 {
		sign = "-"
		ms = -ms
	}
	if ms < 1000 {
		return fmt.Sprintf("%s%dms", sign, ms)
	}
	tenths := (ms + 50) / 100
	if tenths < 600 {
		whole, frac := tenths/10, tenths%10
		if frac == 0 {
			return fmt.Sprintf("%s%ds", sign, whole)
		}
		return fmt.Sprintf("%s%d.%ds", sign, whole, frac)
	}
	totalSec := (ms + 500) / 1000
	units := []struct {
		v int64
		u string
	}{
		{totalSec / 86400, "d"},
		{(totalSec % 86400) / 3600, "h"},
		{(totalSec % 3600) / 60, "m"},
		{totalSec % 60, "s"},
	}
	i := 0
	for i < len(units) && units[i].v == 0 {
		i++
	}
	out := fmt.Sprintf("%s%d%s", sign, units[i].v, units[i].u)
	if i+1 < len(units) && units[i+1].v > 0 {
		out += fmt.Sprintf(" %d%s", units[i+1].v, units[i+1].u)
	}
	return out
}

// asFloat64Strict accepts int/float/decimal values and returns a
// float64 approximation. Returns false for non-numeric types.
func asFloat64Strict(value runtime.Value) (float64, bool) {
	switch v := value.(type) {
	case runtime.SmallInt:
		return float64(v.Value), true
	case runtime.Int:
		f, _ := new(big.Float).SetInt(v.Value).Float64()
		return f, true
	case runtime.Float:
		return v.Value, true
	case runtime.Decimal:
		f, _ := v.Value.Float64()
		return f, true
	}
	return 0, false
}

// registerRandom registers a deterministic pseudo-random number generator
// module backed by Go's math/rand. Use this for sampling, shuffling,
// procedural generation, and any application where reproducibility matters
// (with a fixed seed). For cryptographic randomness - keys, tokens,
// salts - use the `secrets` module instead.
func registerRandom(r *Registry) {
	// A package-level RNG with a process-wide default seed lets the
	// module-level random.* helpers act like Python's `random` while
	// keeping seeded determinism available through random.seed().
	defaultRNG := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	rngFromArg := func(args []runtime.Value, name string, takesOneArg bool) (*mrand.Rand, []runtime.Value, error) {
		// If the first arg is a generator handle (NativeObject of kind
		// "Random") use it; otherwise fall back to the package default.
		if len(args) > 0 {
			if obj, ok := args[0].(runtime.NativeObject); ok && obj.Kind == "Random" {
				randomGeneratorMu.Lock()
				gen, ok := randomGenerators[obj.ID]
				randomGeneratorMu.Unlock()
				if !ok {
					return nil, nil, fmt.Errorf("%s: unknown generator handle", name)
				}
				return gen, args[1:], nil
			}
		}
		_ = takesOneArg
		return defaultRNG, args, nil
	}
	r.Register("random", "seed", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("random.seed expects exactly one int seed")
		}
		seed, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("random.seed seed must be int")
		}
		defaultRNG.Seed(seed)
		return runtime.Null{}, nil
	})
	r.Register("random", "next", func(args []runtime.Value) (runtime.Value, error) {
		gen, rest, err := rngFromArg(args, "random.next", false)
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, fmt.Errorf("random.next expects only an optional generator")
		}
		return runtime.NewInt64(gen.Int63()), nil
	})
	r.Register("random", "intRange", func(args []runtime.Value) (runtime.Value, error) {
		gen, rest, err := rngFromArg(args, "random.intRange", false)
		if err != nil {
			return nil, err
		}
		if len(rest) != 2 {
			return nil, fmt.Errorf("random.intRange expects min and max")
		}
		min, ok := AsInt64(rest[0])
		if !ok {
			return nil, fmt.Errorf("random.intRange min must be int")
		}
		max, ok := AsInt64(rest[1])
		if !ok {
			return nil, fmt.Errorf("random.intRange max must be int")
		}
		if max <= min {
			return nil, fmt.Errorf("random.intRange max must be > min")
		}
		return runtime.NewInt64(min + gen.Int63n(max-min)), nil
	})
	r.Register("random", "float", func(args []runtime.Value) (runtime.Value, error) {
		gen, rest, err := rngFromArg(args, "random.float", false)
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, fmt.Errorf("random.float expects only an optional generator")
		}
		return runtime.Float{Value: gen.Float64()}, nil
	})
	r.Register("random", "bool", func(args []runtime.Value) (runtime.Value, error) {
		gen, rest, err := rngFromArg(args, "random.bool", false)
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, fmt.Errorf("random.bool expects only an optional generator")
		}
		return runtime.Bool{Value: gen.Intn(2) == 1}, nil
	})
	r.Register("random", "choice", func(args []runtime.Value) (runtime.Value, error) {
		gen, rest, err := rngFromArg(args, "random.choice", true)
		if err != nil {
			return nil, err
		}
		if len(rest) != 1 {
			return nil, fmt.Errorf("random.choice expects a non-empty list")
		}
		lst, ok := rest[0].(*runtime.List)
		if !ok || len(lst.Elements) == 0 {
			return nil, fmt.Errorf("random.choice expects a non-empty list")
		}
		return lst.Elements[gen.Intn(len(lst.Elements))], nil
	})
	r.Register("random", "shuffle", func(args []runtime.Value) (runtime.Value, error) {
		gen, rest, err := rngFromArg(args, "random.shuffle", true)
		if err != nil {
			return nil, err
		}
		if len(rest) != 1 {
			return nil, fmt.Errorf("random.shuffle expects a list")
		}
		lst, ok := rest[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("random.shuffle expects a list")
		}
		out := append([]runtime.Value(nil), lst.Elements...)
		gen.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return &runtime.List{Elements: out}, nil
	})
	r.Register("random", "Generator", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("random.Generator expects a single int seed")
		}
		seed, ok := AsInt64(args[0])
		if !ok {
			return nil, fmt.Errorf("random.Generator seed must be int")
		}
		gen := mrand.New(mrand.NewSource(seed))
		randomGeneratorMu.Lock()
		randomNextID++
		id := randomNextID
		randomGenerators[id] = gen
		randomGeneratorMu.Unlock()
		return runtime.NativeObject{Kind: "Random", ID: id}, nil
	})
}

func registerBytes(r *Registry) {
	r.Register("bytes", "fromString", func(args []runtime.Value) (runtime.Value, error) {
		text, err := stringWithOptionalUTF8Encoding(args, "bytes.fromString")
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: []byte(text)}, nil
	})
	r.Register("bytes", "fromList", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("bytes.fromList expects 1 argument")
		}
		list, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("bytes.fromList expects a list of int byte values")
		}
		out := make([]byte, len(list.Elements))
		for i, elem := range list.Elements {
			n, err := byteValueInt(elem, fmt.Sprintf("bytes.fromList element %d", i))
			if err != nil {
				return nil, err
			}
			out[i] = byte(n)
		}
		return runtime.Bytes{Value: out}, nil
	})
	r.Register("bytes", "toString", func(args []runtime.Value) (runtime.Value, error) {
		data, err := bytesWithOptionalUTF8Encoding(args, "bytes.toString")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: string(data)}, nil
	})
	r.Register("bytes", "fromHex", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "bytes.fromHex")
		if err != nil {
			return nil, err
		}
		data, err := hex.DecodeString(text)
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: data}, nil
	})
	r.Register("bytes", "toHex", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleBytes(args, "bytes.toHex")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: hex.EncodeToString(data)}, nil
	})
	r.Register("bytes", "fromBase64", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "bytes.fromBase64")
		if err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: data}, nil
	})
	r.Register("bytes", "toBase64", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleBytes(args, "bytes.toBase64")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: base64.StdEncoding.EncodeToString(data)}, nil
	})
	r.Register("bytes", "fromBase64Url", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "bytes.fromBase64Url")
		if err != nil {
			return nil, err
		}
		data, err := decodeBase64Url(text)
		if err != nil {
			return nil, fmt.Errorf("bytes.fromBase64Url: %v", err)
		}
		return runtime.Bytes{Value: data}, nil
	})
	r.Register("bytes", "toBase64Url", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleBytes(args, "bytes.toBase64Url")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: base64.RawURLEncoding.EncodeToString(data)}, nil
	})
	r.Register("bytes", "concat", func(args []runtime.Value) (runtime.Value, error) {
		out := []byte{}
		for _, arg := range args {
			value, ok := arg.(runtime.Bytes)
			if !ok {
				return nil, fmt.Errorf("bytes.concat arguments must be bytes")
			}
			out = append(out, value.Value...)
		}
		return runtime.Bytes{Value: out}, nil
	})
}

func registerString(r *Registry) {
	r.Register("string", "fromCodePoint", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("string.fromCodePoint expects 1 argument")
		}
		code, err := codePointInt(args[0], "string.fromCodePoint")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: string(rune(code))}, nil
	})
	r.Register("string", "fromCodePoints", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("string.fromCodePoints expects 1 argument")
		}
		list, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("string.fromCodePoints expects a list of int codepoints")
		}
		var sb strings.Builder
		sb.Grow(len(list.Elements) * 2)
		for i, elem := range list.Elements {
			code, err := codePointInt(elem, fmt.Sprintf("string.fromCodePoints element %d", i))
			if err != nil {
				return nil, err
			}
			sb.WriteRune(rune(code))
		}
		return runtime.String{Value: sb.String()}, nil
	})
	r.Register("string", "compare", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("string.compare expects 2 arguments")
		}
		a, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.compare expects string arguments")
		}
		b, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.compare expects string arguments")
		}
		return runtime.SmallInt{Value: int64(strings.Compare(a.Value, b.Value))}, nil
	})
	r.Register("string", "equalsFold", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("string.equalsFold expects 2 arguments")
		}
		a, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.equalsFold expects string arguments")
		}
		b, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("string.equalsFold expects string arguments")
		}
		return runtime.Bool{Value: strings.EqualFold(a.Value, b.Value)}, nil
	})
}

// codePointInt validates a Geblang int argument as a Unicode codepoint
// in the U+0000..U+10FFFF range, rejecting the UTF-16 surrogate half.
func codePointInt(value runtime.Value, label string) (int64, error) {
	var code int64
	switch v := value.(type) {
	case runtime.SmallInt:
		code = v.Value
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, fmt.Errorf("%s: codepoint out of range", label)
		}
		code = v.Value.Int64()
	default:
		return 0, fmt.Errorf("%s expects an int codepoint", label)
	}
	if code < 0 || code > 0x10FFFF || (code >= 0xD800 && code <= 0xDFFF) {
		return 0, fmt.Errorf("%s: %d is not a valid Unicode codepoint", label, code)
	}
	return code, nil
}

func byteValueInt(value runtime.Value, label string) (int64, error) {
	var n int64
	switch v := value.(type) {
	case runtime.SmallInt:
		n = v.Value
	case runtime.Int:
		if !v.Value.IsInt64() {
			return 0, fmt.Errorf("%s: byte value out of range", label)
		}
		n = v.Value.Int64()
	default:
		return 0, fmt.Errorf("%s expects an int byte value", label)
	}
	if n < 0 || n > 255 {
		return 0, fmt.Errorf("%s: %d is not a byte value (0-255)", label, n)
	}
	return n, nil
}

func registerCompress(r *Registry) {
	r.Register("compress", "gzip", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleBytes(args, "compress.gzip")
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		writer := gzip.NewWriter(&out)
		if _, err := writer.Write(data); err != nil {
			_ = writer.Close()
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: out.Bytes()}, nil
	})
	r.Register("compress", "gunzip", func(args []runtime.Value) (runtime.Value, error) {
		data, err := singleBytes(args, "compress.gunzip")
		if err != nil {
			return nil, err
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		out, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		return runtime.Bytes{Value: out}, nil
	})
}

// htmlSanitizePolicy is a reusable UGC allow-list (safe formatting tags,
// strips scripts/styles/event handlers). bluemonday policies are immutable and
// safe for concurrent Sanitize calls.
var htmlSanitizePolicy = bluemonday.UGCPolicy()

func registerEncoding(r *Registry) {
	r.Register("encoding", "base64Encode", func(args []runtime.Value) (runtime.Value, error) {
		data, err := encodingInputBytes(args, "encoding.base64Encode")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: base64.StdEncoding.EncodeToString(data)}, nil
	})
	r.Register("encoding", "base64Decode", func(args []runtime.Value) (runtime.Value, error) {
		s, err := singleString(args, "encoding.base64Decode")
		if err != nil {
			return nil, err
		}
		decoded, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("encoding.base64Decode: %v", err)
		}
		return runtime.String{Value: string(decoded)}, nil
	})
	r.Register("encoding", "urlEncode", func(args []runtime.Value) (runtime.Value, error) {
		s, err := singleString(args, "encoding.urlEncode")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: url.QueryEscape(s)}, nil
	})
	r.Register("encoding", "urlDecode", func(args []runtime.Value) (runtime.Value, error) {
		s, err := singleString(args, "encoding.urlDecode")
		if err != nil {
			return nil, err
		}
		decoded, err := url.QueryUnescape(s)
		if err != nil {
			return nil, fmt.Errorf("encoding.urlDecode: %v", err)
		}
		return runtime.String{Value: decoded}, nil
	})
	r.Register("encoding", "htmlEscape", func(args []runtime.Value) (runtime.Value, error) {
		s, err := singleString(args, "encoding.htmlEscape")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: html.EscapeString(s)}, nil
	})
	r.Register("encoding", "htmlUnescape", func(args []runtime.Value) (runtime.Value, error) {
		s, err := singleString(args, "encoding.htmlUnescape")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: html.UnescapeString(s)}, nil
	})
	r.Register("encoding", "sanitizeHtml", func(args []runtime.Value) (runtime.Value, error) {
		s, err := singleString(args, "encoding.sanitizeHtml")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: htmlSanitizePolicy.Sanitize(s)}, nil
	})
	r.Register("encoding", "base32Encode", base32EncodeFn)
	r.Register("encoding", "base32Decode", base32DecodeFn)
	r.Register("encoding", "base58Encode", base58EncodeFn)
	r.Register("encoding", "base58Decode", base58DecodeFn)
	r.Register("encoding", "base64UrlEncode", func(args []runtime.Value) (runtime.Value, error) {
		data, err := encodingInputBytes(args, "encoding.base64UrlEncode")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: base64.RawURLEncoding.EncodeToString(data)}, nil
	})
	r.Register("encoding", "base64UrlDecode", func(args []runtime.Value) (runtime.Value, error) {
		s, err := singleString(args, "encoding.base64UrlDecode")
		if err != nil {
			return nil, err
		}
		decoded, err := decodeBase64Url(s)
		if err != nil {
			return nil, fmt.Errorf("encoding.base64UrlDecode: %v", err)
		}
		return runtime.String{Value: string(decoded)}, nil
	})
}

// decodeBase64Url accepts both unpadded (RawURLEncoding, JOSE) and padded
// (URLEncoding) input so callers don't have to know which producer emitted
// the string.
func decodeBase64Url(s string) ([]byte, error) {
	trimmed := strings.TrimRight(s, "=")
	if decoded, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// base58Alphabet is the Bitcoin / IPFS alphabet (no 0, O, I, l).
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base32EncodeFn(args []runtime.Value) (runtime.Value, error) {
	data, err := encodingInputBytes(args, "encoding.base32Encode")
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: base32.StdEncoding.EncodeToString(data)}, nil
}

func base32DecodeFn(args []runtime.Value) (runtime.Value, error) {
	s, err := singleString(args, "encoding.base32Decode")
	if err != nil {
		return nil, err
	}
	// Accept both padded and unpadded inputs.
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		decoded, err = base32.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("encoding.base32Decode: %v", err)
		}
	}
	return runtime.Bytes{Value: decoded}, nil
}

func base58EncodeFn(args []runtime.Value) (runtime.Value, error) {
	data, err := encodingInputBytes(args, "encoding.base58Encode")
	if err != nil {
		return nil, err
	}
	return runtime.String{Value: base58Encode(data)}, nil
}

func base58DecodeFn(args []runtime.Value) (runtime.Value, error) {
	s, err := singleString(args, "encoding.base58Decode")
	if err != nil {
		return nil, err
	}
	decoded, err := base58Decode(s)
	if err != nil {
		return nil, fmt.Errorf("encoding.base58Decode: %v", err)
	}
	return runtime.Bytes{Value: decoded}, nil
}

// encodingInputBytes accepts either a string or bytes value as input so the
// new base32/base58 functions can be fed crypto.randomBytes output directly.
func encodingInputBytes(args []runtime.Value, name string) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects one argument", name)
	}
	switch v := args[0].(type) {
	case runtime.String:
		return []byte(v.Value), nil
	case runtime.Bytes:
		return v.Value, nil
	default:
		return nil, fmt.Errorf("%s expects string or bytes, got %s", name, v.TypeName())
	}
}

// base58Encode encodes bytes using the Bitcoin alphabet.
func base58Encode(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	leading := 0
	for _, b := range data {
		if b != 0 {
			break
		}
		leading++
	}
	x := new(big.Int).SetBytes(data)
	base := big.NewInt(58)
	mod := new(big.Int)
	var encoded []byte
	for x.Sign() > 0 {
		x.DivMod(x, base, mod)
		encoded = append(encoded, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < leading; i++ {
		encoded = append(encoded, base58Alphabet[0])
	}
	// reverse
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

// base58Decode decodes a base58 string. Returns an error on invalid characters.
func base58Decode(s string) ([]byte, error) {
	x := new(big.Int)
	base := big.NewInt(58)
	leading := 0
	for _, c := range s {
		if c == rune(base58Alphabet[0]) {
			leading++
			continue
		}
		break
	}
	for _, c := range s {
		idx := strings.IndexRune(base58Alphabet, c)
		if idx < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", c)
		}
		x.Mul(x, base)
		x.Add(x, big.NewInt(int64(idx)))
	}
	decoded := x.Bytes()
	out := make([]byte, leading+len(decoded))
	copy(out[leading:], decoded)
	return out, nil
}

type argon2idParams struct {
	memory      uint32
	time        uint32
	parallelism uint8
	keyLength   uint32
	saltLength  int
}

func defaultArgon2idParams() argon2idParams {
	return argon2idParams{
		memory:      64 * 1024,
		time:        3,
		parallelism: 4,
		keyLength:   32,
		saltLength:  16,
	}
}

func applyArgon2idOptions(params *argon2idParams, options runtime.Dict) error {
	if value, ok := dictInt64(options, "memory"); ok {
		if value < 8 || value > 4*1024*1024 {
			return fmt.Errorf("crypt.argon2idHash memory must be between 8 and 4194304 KiB")
		}
		params.memory = uint32(value)
	}
	if value, ok := dictInt64(options, "time"); ok {
		if value < 1 || value > 32 {
			return fmt.Errorf("crypt.argon2idHash time must be between 1 and 32")
		}
		params.time = uint32(value)
	}
	if value, ok := dictInt64(options, "parallelism"); ok {
		if value < 1 || value > 255 {
			return fmt.Errorf("crypt.argon2idHash parallelism must be between 1 and 255")
		}
		params.parallelism = uint8(value)
	}
	if value, ok := dictInt64(options, "keyLength"); ok {
		if value < 16 || value > 1024 {
			return fmt.Errorf("crypt.argon2idHash keyLength must be between 16 and 1024")
		}
		params.keyLength = uint32(value)
	}
	if value, ok := dictInt64(options, "saltLength"); ok {
		if value < 8 || value > 1024 {
			return fmt.Errorf("crypt.argon2idHash saltLength must be between 8 and 1024")
		}
		params.saltLength = int(value)
	}
	return nil
}

func parseArgon2idHash(encoded string) (argon2idParams, []byte, []byte, error) {
	params, salt, hash, _, err := parseArgon2Hash(encoded)
	return params, salt, hash, err
}

// parseArgon2Hash accepts any of the three Argon2 variants PHP emits:
// $argon2i$, $argon2d$, and $argon2id$. Returns the variant name so callers
// can dispatch to the right argon2 derivation function.
func parseArgon2Hash(encoded string) (argon2idParams, []byte, []byte, string, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return argon2idParams{}, nil, nil, "", fmt.Errorf("invalid argon2 hash")
	}
	variant := parts[1]
	if variant != "argon2id" && variant != "argon2i" && variant != "argon2d" {
		return argon2idParams{}, nil, nil, "", fmt.Errorf("invalid argon2 hash")
	}
	if parts[2] != "v=19" {
		return argon2idParams{}, nil, nil, "", fmt.Errorf("invalid argon2 hash")
	}
	params := argon2idParams{keyLength: 32}
	for _, item := range strings.Split(parts[3], ",") {
		pair := strings.SplitN(item, "=", 2)
		if len(pair) != 2 {
			return argon2idParams{}, nil, nil, "", fmt.Errorf("invalid argon2 parameters")
		}
		value, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil {
			return argon2idParams{}, nil, nil, "", err
		}
		switch pair[0] {
		case "m":
			params.memory = uint32(value)
		case "t":
			params.time = uint32(value)
		case "p":
			if value > 255 {
				return argon2idParams{}, nil, nil, "", fmt.Errorf("invalid argon2 parallelism")
			}
			params.parallelism = uint8(value)
		default:
			return argon2idParams{}, nil, nil, "", fmt.Errorf("unknown argon2 parameter")
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2idParams{}, nil, nil, "", err
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2idParams{}, nil, nil, "", err
	}
	if params.memory == 0 || params.time == 0 || params.parallelism == 0 || len(salt) == 0 || len(hash) == 0 {
		return argon2idParams{}, nil, nil, "", fmt.Errorf("invalid argon2 hash")
	}
	params.keyLength = uint32(len(hash))
	return params, salt, hash, variant, nil
}

func registerURL(r *Registry) {
	r.Register("url", "URL", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("url.URL expects string or dict")
		}
		switch value := args[0].(type) {
		case runtime.String:
			parsed, err := url.Parse(value.Value)
			if err != nil {
				return nil, fmt.Errorf("url.URL: %v", err)
			}
			return runtime.URLValue{Raw: parsed.String()}, nil
		case runtime.Dict:
			text, err := buildURLString(value)
			if err != nil {
				return nil, fmt.Errorf("url.URL: %v", err)
			}
			return runtime.URLValue{Raw: text}, nil
		default:
			return nil, fmt.Errorf("url.URL expects string or dict")
		}
	})
	r.Register("url", "parse", func(args []runtime.Value) (runtime.Value, error) {
		raw, err := singleString(args, "url.parse")
		if err != nil {
			return nil, err
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("url.parse: %v", err)
		}
		return urlPartsDict(parsed), nil
	})
	r.Register("url", "stringify", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("url.stringify expects exactly one argument")
		}
		parts, ok := args[0].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("url.stringify expects dict")
		}
		text, err := buildURLString(parts)
		if err != nil {
			return nil, fmt.Errorf("url.stringify: %v", err)
		}
		return runtime.String{Value: text}, nil
	})
	r.Register("url", "encode", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "url.encode")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: url.QueryEscape(text)}, nil
	})
	r.Register("url", "decode", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleString(args, "url.decode")
		if err != nil {
			return nil, err
		}
		decoded, err := url.QueryUnescape(text)
		if err != nil {
			return nil, fmt.Errorf("url.decode: %v", err)
		}
		return runtime.String{Value: decoded}, nil
	})
	r.Register("url", "joinPath", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("url.joinPath expects base and optional path parts")
		}
		base, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("url.joinPath base must be string")
		}
		u, err := url.Parse(base.Value)
		if err != nil {
			return nil, fmt.Errorf("url.joinPath: %v", err)
		}
		if len(args) == 1 {
			return runtime.String{Value: u.String()}, nil
		}
		parts := []string{u.Path}
		for _, arg := range args[1:] {
			part, ok := arg.(runtime.String)
			if !ok {
				return nil, fmt.Errorf("url.joinPath parts must be strings")
			}
			parts = append(parts, part.Value)
		}
		u.Path = pathlib.Join(parts...)
		if strings.HasSuffix(parts[len(parts)-1], "/") && !strings.HasSuffix(u.Path, "/") {
			u.Path += "/"
		}
		return runtime.String{Value: u.String()}, nil
	})
}

func URLMethod(receiver runtime.URLValue, name string, args []runtime.Value) (runtime.Value, error) {
	parsed, err := url.Parse(receiver.Raw)
	if err != nil {
		return nil, fmt.Errorf("url.URL.%s: %v", name, err)
	}
	switch name {
	case "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.toString expects no arguments")
		}
		return runtime.String{Value: parsed.String()}, nil
	case "toDict":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.toDict expects no arguments")
		}
		return urlPartsDict(parsed), nil
	case "scheme":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.scheme expects no arguments")
		}
		return runtime.String{Value: parsed.Scheme}, nil
	case "host":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.host expects no arguments")
		}
		return runtime.String{Value: parsed.Hostname()}, nil
	case "port":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.port expects no arguments")
		}
		return runtime.String{Value: parsed.Port()}, nil
	case "path":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.path expects no arguments")
		}
		return runtime.String{Value: parsed.Path}, nil
	case "query":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.query expects no arguments")
		}
		return queryDict(parsed.Query()), nil
	case "fragment":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.fragment expects no arguments")
		}
		return runtime.String{Value: parsed.Fragment}, nil
	case "withScheme":
		text, err := singleString(args, "url.URL.withScheme")
		if err != nil {
			return nil, err
		}
		parsed.Scheme = text
		return runtime.URLValue{Raw: parsed.String()}, nil
	case "withHost":
		text, err := singleString(args, "url.URL.withHost")
		if err != nil {
			return nil, err
		}
		parsed.Host = text
		return runtime.URLValue{Raw: parsed.String()}, nil
	case "withPath":
		text, err := singleString(args, "url.URL.withPath")
		if err != nil {
			return nil, err
		}
		parsed.Path = text
		return runtime.URLValue{Raw: parsed.String()}, nil
	case "withQuery":
		if len(args) != 1 {
			return nil, fmt.Errorf("url.URL.withQuery expects dict or string")
		}
		switch value := args[0].(type) {
		case runtime.String:
			parsed.RawQuery = strings.TrimPrefix(value.Value, "?")
		case runtime.Dict:
			query, err := queryValues(value)
			if err != nil {
				return nil, fmt.Errorf("url.URL.withQuery: %v", err)
			}
			parsed.RawQuery = query.Encode()
		default:
			return nil, fmt.Errorf("url.URL.withQuery expects dict or string")
		}
		return runtime.URLValue{Raw: parsed.String()}, nil
	case "withFragment":
		text, err := singleString(args, "url.URL.withFragment")
		if err != nil {
			return nil, err
		}
		parsed.Fragment = text
		return runtime.URLValue{Raw: parsed.String()}, nil
	case "resolve":
		if len(args) != 1 {
			return nil, fmt.Errorf("url.URL.resolve expects URL or string")
		}
		ref, err := urlFromValue(args[0])
		if err != nil {
			return nil, fmt.Errorf("url.URL.resolve: %v", err)
		}
		return runtime.URLValue{Raw: parsed.ResolveReference(ref).String()}, nil
	case "normalize":
		if len(args) != 0 {
			return nil, fmt.Errorf("url.URL.normalize expects no arguments")
		}
		parsed.Path = pathlib.Clean(parsed.Path)
		if parsed.Path == "." {
			parsed.Path = ""
		}
		parsed.RawQuery = parsed.Query().Encode()
		return runtime.URLValue{Raw: parsed.String()}, nil
	default:
		return nil, fmt.Errorf("url.URL has no method %s", name)
	}
}

func buildURLString(parts runtime.Dict) (string, error) {
	u := url.URL{
		Scheme:   dictString(parts, "scheme"),
		Path:     dictString(parts, "path"),
		Fragment: dictString(parts, "fragment"),
	}
	host := dictString(parts, "host")
	port := dictString(parts, "port")
	if host != "" && port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	if queryValue, ok := dictLookup(parts, "query"); ok {
		query, err := queryValues(queryValue)
		if err != nil {
			return "", err
		}
		u.RawQuery = query.Encode()
	}
	return u.String(), nil
}

func urlFromValue(value runtime.Value) (*url.URL, error) {
	switch value := value.(type) {
	case runtime.URLValue:
		return url.Parse(value.Raw)
	case runtime.String:
		return url.Parse(value.Value)
	default:
		return nil, fmt.Errorf("value must be url.URL or string")
	}
}

func urlPartsDict(parsed *url.URL) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putString := func(key string, value string) {
		keyValue := runtime.String{Value: key}
		entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: runtime.String{Value: value}}
	}
	putString("scheme", parsed.Scheme)
	putString("host", parsed.Hostname())
	putString("port", parsed.Port())
	putString("path", parsed.Path)
	keyValue := runtime.String{Value: "query"}
	entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: queryDict(parsed.Query())}
	putString("fragment", parsed.Fragment)
	return runtime.Dict{Entries: entries}
}

func queryDict(query url.Values) runtime.Dict {
	queryEntries := map[string]runtime.DictEntry{}
	for key, values := range query {
		keyValue := runtime.String{Value: key}
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
		queryEntries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
	}
	return runtime.Dict{Entries: queryEntries}
}

func registerUUID(r *Registry) {
	r.Register("uuid", "v1", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.v1 expects no arguments")
		}
		u, err := googleuuid.NewUUID()
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "v4", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.v4 expects no arguments")
		}
		var data [16]byte
		if _, err := rand.Read(data[:]); err != nil {
			return nil, err
		}
		data[6] = (data[6] & 0x0f) | 0x40
		data[8] = (data[8] & 0x3f) | 0x80
		return runtime.String{Value: formatUUID(data)}, nil
	})
	r.Register("uuid", "v7", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.v7 expects no arguments")
		}
		var data [16]byte
		if _, err := rand.Read(data[:]); err != nil {
			return nil, err
		}
		ms := time.Now().UnixMilli()
		data[0] = byte(ms >> 40)
		data[1] = byte(ms >> 32)
		data[2] = byte(ms >> 24)
		data[3] = byte(ms >> 16)
		data[4] = byte(ms >> 8)
		data[5] = byte(ms)
		data[6] = (data[6] & 0x0f) | 0x70
		data[8] = (data[8] & 0x3f) | 0x80
		return runtime.String{Value: formatUUID(data)}, nil
	})
	r.Register("uuid", "v3", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("uuid.v3 expects 2 arguments: namespace (string), name (string)")
		}
		ns, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v3: namespace must be a string")
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v3: name must be a string")
		}
		nsUUID, err := googleuuid.Parse(ns.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.v3: invalid namespace UUID: %w", err)
		}
		u := googleuuid.NewMD5(nsUUID, []byte(name.Value))
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "v5", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("uuid.v5 expects 2 arguments: namespace (string), name (string)")
		}
		ns, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v5: namespace must be a string")
		}
		name, ok := args[1].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.v5: name must be a string")
		}
		nsUUID, err := googleuuid.Parse(ns.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.v5: invalid namespace UUID: %w", err)
		}
		u := googleuuid.NewSHA1(nsUUID, []byte(name.Value))
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "parse", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.parse expects 1 argument")
		}
		s, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.parse: argument must be a string")
		}
		u, err := googleuuid.Parse(s.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.parse: %w", err)
		}
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "isValid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.isValid expects 1 argument")
		}
		s, ok := args[0].(runtime.String)
		if !ok {
			return runtime.Bool{Value: false}, nil
		}
		return runtime.Bool{Value: googleuuid.Validate(s.Value) == nil}, nil
	})
	r.Register("uuid", "nil", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.nil expects no arguments")
		}
		return runtime.String{Value: "00000000-0000-0000-0000-000000000000"}, nil
	})
	r.Register("uuid", "toBytes", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.toBytes expects 1 argument")
		}
		s, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("uuid.toBytes: argument must be a string")
		}
		u, err := googleuuid.Parse(s.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.toBytes: %w", err)
		}
		return runtime.Bytes{Value: u[:]}, nil
	})
	r.Register("uuid", "fromBytes", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("uuid.fromBytes expects 1 argument")
		}
		b, ok := args[0].(runtime.Bytes)
		if !ok {
			return nil, fmt.Errorf("uuid.fromBytes: argument must be bytes")
		}
		u, err := googleuuid.FromBytes(b.Value)
		if err != nil {
			return nil, fmt.Errorf("uuid.fromBytes: %w", err)
		}
		return runtime.String{Value: u.String()}, nil
	})
	r.Register("uuid", "namespaceDNS", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceDNS.String()}, nil
	})
	r.Register("uuid", "namespaceURL", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceURL.String()}, nil
	})
	r.Register("uuid", "namespaceOID", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceOID.String()}, nil
	})
	r.Register("uuid", "namespaceX500", func(args []runtime.Value) (runtime.Value, error) {
		return runtime.String{Value: googleuuid.NameSpaceX500.String()}, nil
	})
	r.Register("uuid", "ulid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("uuid.ulid expects no arguments")
		}
		var randBytes [10]byte
		if _, err := rand.Read(randBytes[:]); err != nil {
			return nil, err
		}
		return runtime.String{Value: encodeULID(time.Now().UnixMilli(), randBytes)}, nil
	})
}

func formatUUID(data [16]byte) string {
	encoded := hex.EncodeToString(data[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func encodeULID(tsMs int64, r [10]byte) string {
	var b [26]byte
	// 10 chars encode 48-bit timestamp (5 bits each, MSB first)
	b[0] = ulidAlphabet[(tsMs>>45)&0x1F]
	b[1] = ulidAlphabet[(tsMs>>40)&0x1F]
	b[2] = ulidAlphabet[(tsMs>>35)&0x1F]
	b[3] = ulidAlphabet[(tsMs>>30)&0x1F]
	b[4] = ulidAlphabet[(tsMs>>25)&0x1F]
	b[5] = ulidAlphabet[(tsMs>>20)&0x1F]
	b[6] = ulidAlphabet[(tsMs>>15)&0x1F]
	b[7] = ulidAlphabet[(tsMs>>10)&0x1F]
	b[8] = ulidAlphabet[(tsMs>>5)&0x1F]
	b[9] = ulidAlphabet[tsMs&0x1F]
	// 16 chars encode 80-bit randomness (5 bits each, MSB first)
	b[10] = ulidAlphabet[r[0]>>3]
	b[11] = ulidAlphabet[((r[0]&0x07)<<2)|(r[1]>>6)]
	b[12] = ulidAlphabet[(r[1]>>1)&0x1F]
	b[13] = ulidAlphabet[((r[1]&0x01)<<4)|(r[2]>>4)]
	b[14] = ulidAlphabet[((r[2]&0x0F)<<1)|(r[3]>>7)]
	b[15] = ulidAlphabet[(r[3]>>2)&0x1F]
	b[16] = ulidAlphabet[((r[3]&0x03)<<3)|(r[4]>>5)]
	b[17] = ulidAlphabet[r[4]&0x1F]
	b[18] = ulidAlphabet[r[5]>>3]
	b[19] = ulidAlphabet[((r[5]&0x07)<<2)|(r[6]>>6)]
	b[20] = ulidAlphabet[(r[6]>>1)&0x1F]
	b[21] = ulidAlphabet[((r[6]&0x01)<<4)|(r[7]>>4)]
	b[22] = ulidAlphabet[((r[7]&0x0F)<<1)|(r[8]>>7)]
	b[23] = ulidAlphabet[(r[8]>>2)&0x1F]
	b[24] = ulidAlphabet[((r[8]&0x03)<<3)|(r[9]>>5)]
	b[25] = ulidAlphabet[r[9]&0x1F]
	return string(b[:])
}

func HTTPCookieFromValue(value runtime.Value) (runtime.HTTPCookie, error) {
	switch value := value.(type) {
	case runtime.HTTPCookie:
		return value, nil
	case runtime.String:
		response := nethttp.Response{Header: nethttp.Header{}}
		response.Header.Add("Set-Cookie", value.Value)
		cookies := response.Cookies()
		if len(cookies) == 0 {
			return runtime.HTTPCookie{}, fmt.Errorf("invalid Set-Cookie header")
		}
		return httpCookieFromNative(cookies[0]), nil
	case runtime.Dict:
		cookie := runtime.HTTPCookie{}
		if name := dictString(value, "name"); name != "" {
			cookie.Name = name
		}
		if cookie.Name == "" {
			return cookie, fmt.Errorf("cookie name is required")
		}
		cookie.Value = dictString(value, "value")
		cookie.Path = dictString(value, "path")
		cookie.Domain = dictString(value, "domain")
		if maxAge, ok := dictInt64(value, "maxAge"); ok {
			cookie.MaxAge = maxAge
		}
		if expires, ok := dictInt64(value, "expires"); ok {
			cookie.Expires = expires
		}
		cookie.Secure = dictBool(value, "secure")
		cookie.HTTPOnly = dictBool(value, "httpOnly")
		cookie.SameSite = dictString(value, "sameSite")
		if err := validateSameSite(cookie.SameSite); err != nil {
			return cookie, err
		}
		return cookie, nil
	default:
		return runtime.HTTPCookie{}, fmt.Errorf("cookie must be dict, string, or http.Cookie")
	}
}

func HTTPCookieMethod(receiver runtime.HTTPCookie, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "name":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.name expects no arguments")
		}
		return runtime.String{Value: receiver.Name}, nil
	case "value":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.value expects no arguments")
		}
		return runtime.String{Value: receiver.Value}, nil
	case "path":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.path expects no arguments")
		}
		return runtime.String{Value: receiver.Path}, nil
	case "domain":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.domain expects no arguments")
		}
		return runtime.String{Value: receiver.Domain}, nil
	case "maxAge":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.maxAge expects no arguments")
		}
		return runtime.NewInt64(receiver.MaxAge), nil
	case "expires":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.expires expects no arguments")
		}
		if receiver.Expires == 0 {
			return runtime.Null{}, nil
		}
		return runtime.NewInt64(receiver.Expires), nil
	case "secure":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.secure expects no arguments")
		}
		return runtime.Bool{Value: receiver.Secure}, nil
	case "httpOnly":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.httpOnly expects no arguments")
		}
		return runtime.Bool{Value: receiver.HTTPOnly}, nil
	case "sameSite":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.sameSite expects no arguments")
		}
		return runtime.String{Value: receiver.SameSite}, nil
	case "withValue":
		text, err := singleString(args, "http.Cookie.withValue")
		if err != nil {
			return nil, err
		}
		receiver.Value = text
		return receiver, nil
	case "withPath":
		text, err := singleString(args, "http.Cookie.withPath")
		if err != nil {
			return nil, err
		}
		receiver.Path = text
		return receiver, nil
	case "withDomain":
		text, err := singleString(args, "http.Cookie.withDomain")
		if err != nil {
			return nil, err
		}
		receiver.Domain = text
		return receiver, nil
	case "withMaxAge":
		seconds, err := singleInt64(args, "http.Cookie.withMaxAge")
		if err != nil {
			return nil, err
		}
		receiver.MaxAge = seconds
		return receiver, nil
	case "withExpires":
		seconds, err := singleInt64(args, "http.Cookie.withExpires")
		if err != nil {
			return nil, err
		}
		receiver.Expires = seconds
		return receiver, nil
	case "withSecure":
		value, err := singleBool(args, "http.Cookie.withSecure")
		if err != nil {
			return nil, err
		}
		receiver.Secure = value
		return receiver, nil
	case "withHttpOnly":
		value, err := singleBool(args, "http.Cookie.withHttpOnly")
		if err != nil {
			return nil, err
		}
		receiver.HTTPOnly = value
		return receiver, nil
	case "withSameSite":
		text, err := singleString(args, "http.Cookie.withSameSite")
		if err != nil {
			return nil, err
		}
		if err := validateSameSite(text); err != nil {
			return nil, err
		}
		receiver.SameSite = text
		return receiver, nil
	case "toHeader", "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.%s expects no arguments", name)
		}
		return runtime.String{Value: nativeCookie(receiver).String()}, nil
	case "toDict":
		if len(args) != 0 {
			return nil, fmt.Errorf("http.Cookie.toDict expects no arguments")
		}
		return httpCookieToDict(receiver), nil
	default:
		return nil, fmt.Errorf("http.Cookie has no method %s", name)
	}
}

func httpCookieFromNative(cookie *nethttp.Cookie) runtime.HTTPCookie {
	out := runtime.HTTPCookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Path:     cookie.Path,
		Domain:   cookie.Domain,
		MaxAge:   int64(cookie.MaxAge),
		Secure:   cookie.Secure,
		HTTPOnly: cookie.HttpOnly,
		SameSite: sameSiteString(cookie.SameSite),
	}
	if !cookie.Expires.IsZero() {
		out.Expires = cookie.Expires.Unix()
	}
	return out
}

func nativeCookie(cookie runtime.HTTPCookie) *nethttp.Cookie {
	out := &nethttp.Cookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Path:     cookie.Path,
		Domain:   cookie.Domain,
		MaxAge:   int(cookie.MaxAge),
		Secure:   cookie.Secure,
		HttpOnly: cookie.HTTPOnly,
		SameSite: nativeSameSite(cookie.SameSite),
	}
	if cookie.Expires != 0 {
		out.Expires = time.Unix(cookie.Expires, 0).UTC()
	}
	return out
}

func httpCookieToDict(cookie runtime.HTTPCookie) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	put := func(key string, value runtime.Value) {
		keyValue := runtime.String{Value: key}
		entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
	}
	put("name", runtime.String{Value: cookie.Name})
	put("value", runtime.String{Value: cookie.Value})
	put("path", runtime.String{Value: cookie.Path})
	put("domain", runtime.String{Value: cookie.Domain})
	if cookie.Expires == 0 {
		put("expires", runtime.Null{})
	} else {
		put("expires", runtime.NewInt64(cookie.Expires))
	}
	put("maxAge", runtime.NewInt64(cookie.MaxAge))
	put("secure", runtime.Bool{Value: cookie.Secure})
	put("httpOnly", runtime.Bool{Value: cookie.HTTPOnly})
	put("sameSite", runtime.String{Value: cookie.SameSite})
	return runtime.Dict{Entries: entries}
}

func dictBool(dict runtime.Dict, key string) bool {
	value, ok := dictLookup(dict, key)
	if !ok {
		return false
	}
	boolValue, ok := value.(runtime.Bool)
	return ok && boolValue.Value
}

func singleBool(args []runtime.Value, label string) (bool, error) {
	if len(args) != 1 {
		return false, fmt.Errorf("%s expects exactly one argument", label)
	}
	value, ok := args[0].(runtime.Bool)
	if !ok {
		return false, fmt.Errorf("%s argument must be bool", label)
	}
	return value.Value, nil
}

func validateSameSite(value string) error {
	switch strings.ToLower(value) {
	case "", "default", "lax", "strict", "none":
		return nil
	default:
		return fmt.Errorf("sameSite must be default, lax, strict, or none")
	}
}

func nativeSameSite(value string) nethttp.SameSite {
	switch strings.ToLower(value) {
	case "lax":
		return nethttp.SameSiteLaxMode
	case "strict":
		return nethttp.SameSiteStrictMode
	case "none":
		return nethttp.SameSiteNoneMode
	default:
		return nethttp.SameSiteDefaultMode
	}
}

func sameSiteString(value nethttp.SameSite) string {
	switch value {
	case nethttp.SameSiteLaxMode:
		return "lax"
	case nethttp.SameSiteStrictMode:
		return "strict"
	case nethttp.SameSiteNoneMode:
		return "none"
	default:
		return "default"
	}
}

func dictLookup(dict runtime.Dict, key string) (runtime.Value, bool) {
	keyValue := runtime.String{Value: key}
	entry, ok := dict.GetEntry(DictKey(keyValue))
	if !ok {
		return nil, false
	}
	return entry.Value, true
}

func dictString(dict runtime.Dict, key string) string {
	value, ok := dictLookup(dict, key)
	if !ok {
		return ""
	}
	text, ok := value.(runtime.String)
	if !ok {
		return ""
	}
	return text.Value
}

func dictInt64(dict runtime.Dict, key string) (int64, bool) {
	value, ok := dictLookup(dict, key)
	if !ok {
		return 0, false
	}
	n, ok := AsInt64(value)
	return n, ok
}

func queryValues(value runtime.Value) (url.Values, error) {
	query := url.Values{}
	dict, ok := value.(runtime.Dict)
	if !ok {
		return query, fmt.Errorf("query must be dict")
	}
	for _, dk := range dict.EntryKeys() {
		entry, _ := dict.GetEntry(dk)
		key, ok := entry.Key.(runtime.String)
		if !ok {
			return query, fmt.Errorf("query keys must be strings")
		}
		switch value := entry.Value.(type) {
		case runtime.String:
			query.Add(key.Value, value.Value)
		case *runtime.List:
			for _, item := range value.Elements {
				text, ok := item.(runtime.String)
				if !ok {
					return query, fmt.Errorf("query list values must be strings")
				}
				query.Add(key.Value, text.Value)
			}
		default:
			return query, fmt.Errorf("query values must be strings or lists of strings")
		}
	}
	return query, nil
}

// reMatchDict builds the result dict for re.match / re.matchAll:
//
//	"text"   - the whole match.
//	"groups" - list of all capture groups in order (groups[0] = whole match,
//	           groups[1..] = numbered subexpressions).
//	"named"  - dict mapping named groups to their captured text.
func reMatchDict(re *regexp.Regexp, match []string) runtime.Dict {
	textKey := runtime.String{Value: "text"}
	groupsKey := runtime.String{Value: "groups"}
	namedKey := runtime.String{Value: "named"}

	groupsElems := make([]runtime.Value, len(match))
	for i, g := range match {
		groupsElems[i] = runtime.String{Value: g}
	}

	namedEntries := map[string]runtime.DictEntry{}
	for i, name := range re.SubexpNames() {
		if name == "" || i >= len(match) {
			continue
		}
		nameKey := runtime.String{Value: name}
		namedEntries[DictKey(nameKey)] = runtime.DictEntry{Key: nameKey, Value: runtime.String{Value: match[i]}}
	}

	entries := map[string]runtime.DictEntry{
		DictKey(textKey):   {Key: textKey, Value: runtime.String{Value: match[0]}},
		DictKey(groupsKey): {Key: groupsKey, Value: &runtime.List{Elements: groupsElems}},
		DictKey(namedKey):  {Key: namedKey, Value: runtime.Dict{Entries: namedEntries}},
	}
	return runtime.Dict{Entries: entries}
}

var (
	regexCache        atomic.Pointer[map[string]*regexp.Regexp]
	regexCacheWriteMu sync.Mutex
	// regexLastHit short-circuits the map (and the per-call string
	// hash) for the dominant pattern-reuse-in-a-loop shape.
	regexLastHit atomic.Pointer[regexCacheEntry]
)

type regexCacheEntry struct {
	pattern string
	re      *regexp.Regexp
}

const regexCacheMaxEntries = 256

func init() {
	empty := map[string]*regexp.Regexp{}
	regexCache.Store(&empty)
}

// CompileCachedRegex exposes the package-internal regex cache to other
// packages that want the same compile-once behaviour.
func CompileCachedRegex(pattern string) (*regexp.Regexp, error) {
	return compileCachedRegex(pattern)
}

func compileCachedRegex(pattern string) (*regexp.Regexp, error) {
	if last := regexLastHit.Load(); last != nil && last.pattern == pattern {
		return last.re, nil
	}
	current := *regexCache.Load()
	if re, ok := current[pattern]; ok {
		regexLastHit.Store(&regexCacheEntry{pattern: pattern, re: re})
		return re, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCacheWriteMu.Lock()
	defer regexCacheWriteMu.Unlock()
	current = *regexCache.Load()
	if existing, ok := current[pattern]; ok {
		return existing, nil
	}
	next := make(map[string]*regexp.Regexp, len(current)+1)
	if len(current) < regexCacheMaxEntries {
		for k, v := range current {
			next[k] = v
		}
	}
	next[pattern] = re
	regexCache.Store(&next)
	regexLastHit.Store(&regexCacheEntry{pattern: pattern, re: re})
	return re, nil
}

func registerRe(r *Registry) {
	twoStrings := func(args []runtime.Value, label string) (string, string, error) {
		if len(args) != 2 {
			return "", "", fmt.Errorf("%s expects two string arguments", label)
		}
		pattern, ok1 := args[0].(runtime.String)
		text, ok2 := args[1].(runtime.String)
		if !ok1 || !ok2 {
			return "", "", fmt.Errorf("%s arguments must be strings", label)
		}
		return pattern.Value, text.Value, nil
	}

	reFn := func(name string, body func(re *regexp.Regexp, text string) runtime.Value) {
		r.Register("re", name, func(args []runtime.Value) (runtime.Value, error) {
			pattern, text, err := twoStrings(args, "re."+name)
			if err != nil {
				return nil, err
			}
			re, err := compileCachedRegex(pattern)
			if err != nil {
				return nil, fmt.Errorf("re.%s: invalid pattern: %v", name, err)
			}
			return body(re, text), nil
		})
	}
	reFn("test", reTestCore)
	reFn("find", reFindCore)
	reFn("findAll", reFindAllCore)
	reFn("match", reMatchCore)
	reFn("matchAll", reMatchAllCore)
	reFn("split", reSplitCore)
	r.Register("re", "replace", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("re.replace expects (pattern, replacement, text)")
		}
		pattern, ok1 := args[0].(runtime.String)
		repl, ok2 := args[1].(runtime.String)
		text, ok3 := args[2].(runtime.String)
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("re.replace arguments must be strings")
		}
		re, err := compileCachedRegex(pattern.Value)
		if err != nil {
			return nil, fmt.Errorf("re.replace: invalid pattern: %v", err)
		}
		return reReplaceCore(re, repl.Value, text.Value), nil
	})
}

// re operation cores, shared by the module functions and the compiled
// Pattern handle. Each takes an already-compiled regex.
func reTestCore(re *regexp.Regexp, text string) runtime.Value {
	return runtime.Bool{Value: re.MatchString(text)}
}

func reFindCore(re *regexp.Regexp, text string) runtime.Value {
	match := re.FindString(text)
	if match == "" && !re.MatchString(text) {
		return runtime.Null{}
	}
	return runtime.String{Value: match}
}

func reFindAllCore(re *regexp.Regexp, text string) runtime.Value {
	matches := re.FindAllString(text, -1)
	elements := make([]runtime.Value, len(matches))
	for i, m := range matches {
		elements[i] = runtime.String{Value: m}
	}
	return &runtime.List{Elements: elements}
}

func reMatchCore(re *regexp.Regexp, text string) runtime.Value {
	match := re.FindStringSubmatch(text)
	if match == nil {
		return runtime.Null{}
	}
	return reMatchDict(re, match)
}

func reMatchAllCore(re *regexp.Regexp, text string) runtime.Value {
	all := re.FindAllStringSubmatch(text, -1)
	elements := make([]runtime.Value, 0, len(all))
	for _, m := range all {
		elements = append(elements, reMatchDict(re, m))
	}
	return &runtime.List{Elements: elements}
}

func reReplaceCore(re *regexp.Regexp, repl, text string) runtime.Value {
	return runtime.String{Value: re.ReplaceAllString(text, repl)}
}

func reSplitCore(re *regexp.Regexp, text string) runtime.Value {
	parts := re.Split(text, -1)
	elements := make([]runtime.Value, len(parts))
	for i, p := range parts {
		elements[i] = runtime.String{Value: p}
	}
	return &runtime.List{Elements: elements}
}

var gfmMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(goldparser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(goldhtml.WithUnsafe()),
)

func registerMarkdown(r *Registry) {
	r.Register("markdown", "parse", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleStringArg(args, "markdown.parse")
		if err != nil {
			return nil, err
		}
		return markdownParse(text), nil
	})
	r.Register("markdown", "renderHtml", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleStringArg(args, "markdown.renderHtml")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: markdownRenderHTML(text)}, nil
	})
	r.Register("markdown", "stripText", func(args []runtime.Value) (runtime.Value, error) {
		text, err := singleStringArg(args, "markdown.stripText")
		if err != nil {
			return nil, err
		}
		return runtime.String{Value: markdownStripText(text)}, nil
	})
}

func singleStringArg(args []runtime.Value, label string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects one string argument", label)
	}
	text, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s argument must be string", label)
	}
	return text.Value, nil
}

func markdownRenderHTML(src string) string {
	var buf bytes.Buffer
	_ = gfmMarkdown.Convert([]byte(src), &buf)
	return buf.String()
}

func markdownParse(src string) *runtime.List {
	reader := goldtext.NewReader([]byte(src))
	doc := gfmMarkdown.Parser().Parse(reader)
	source := []byte(src)
	var results []runtime.Value
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if block, ok := goldmarkNodeToBlock(n, source); ok {
			results = append(results, block)
		}
	}
	if results == nil {
		results = []runtime.Value{}
	}
	return &runtime.List{Elements: results}
}

func markdownStripText(src string) string {
	reader := goldtext.NewReader([]byte(src))
	doc := gfmMarkdown.Parser().Parse(reader)
	source := []byte(src)
	var parts []string
	_ = goldast.Walk(doc, func(n goldast.Node, entering bool) (goldast.WalkStatus, error) {
		if !entering {
			return goldast.WalkContinue, nil
		}
		if t, ok := n.(*goldast.Text); ok {
			parts = append(parts, string(t.Segment.Value(source)))
		}
		return goldast.WalkContinue, nil
	})
	return strings.Join(parts, "")
}

func putMarkdownDict(entries map[string]runtime.DictEntry, key string, value runtime.Value) {
	keyValue := runtime.String{Value: key}
	entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: value}
}

func goldmarkNodeToBlock(n goldast.Node, source []byte) (runtime.Dict, bool) {
	entries := map[string]runtime.DictEntry{}
	switch n.Kind() {
	case goldast.KindHeading:
		heading := n.(*goldast.Heading)
		putMarkdownDict(entries, "type", runtime.String{Value: "heading"})
		putMarkdownDict(entries, "level", runtime.NewInt64(int64(heading.Level)))
		putMarkdownDict(entries, "text", runtime.String{Value: goldmarkNodeText(n, source)})
		return runtime.Dict{Entries: entries}, true
	case goldast.KindParagraph:
		putMarkdownDict(entries, "type", runtime.String{Value: "paragraph"})
		putMarkdownDict(entries, "text", runtime.String{Value: goldmarkNodeText(n, source)})
		return runtime.Dict{Entries: entries}, true
	case goldast.KindFencedCodeBlock:
		fcb := n.(*goldast.FencedCodeBlock)
		lang := ""
		if l := fcb.Language(source); l != nil {
			lang = string(l)
		}
		putMarkdownDict(entries, "type", runtime.String{Value: "code"})
		putMarkdownDict(entries, "lang", runtime.String{Value: lang})
		putMarkdownDict(entries, "code", runtime.String{Value: goldmarkCodeContent(n, source)})
		return runtime.Dict{Entries: entries}, true
	case goldast.KindCodeBlock:
		putMarkdownDict(entries, "type", runtime.String{Value: "code"})
		putMarkdownDict(entries, "lang", runtime.String{Value: ""})
		putMarkdownDict(entries, "code", runtime.String{Value: goldmarkCodeContent(n, source)})
		return runtime.Dict{Entries: entries}, true
	case goldast.KindList:
		return goldmarkListToBlock(n.(*goldast.List), source), true
	case extast.KindTable:
		return goldmarkTableToBlock(n, source), true
	case goldast.KindBlockquote:
		putMarkdownDict(entries, "type", runtime.String{Value: "blockquote"})
		putMarkdownDict(entries, "text", runtime.String{Value: goldmarkNodeText(n, source)})
		return runtime.Dict{Entries: entries}, true
	case goldast.KindThematicBreak:
		putMarkdownDict(entries, "type", runtime.String{Value: "hr"})
		return runtime.Dict{Entries: entries}, true
	case goldast.KindHTMLBlock:
		putMarkdownDict(entries, "type", runtime.String{Value: "html"})
		putMarkdownDict(entries, "html", runtime.String{Value: goldmarkCodeContent(n, source)})
		return runtime.Dict{Entries: entries}, true
	}
	return runtime.Dict{}, false
}

func goldmarkNodeText(n goldast.Node, source []byte) string {
	var parts []string
	_ = goldast.Walk(n, func(child goldast.Node, entering bool) (goldast.WalkStatus, error) {
		if !entering {
			return goldast.WalkContinue, nil
		}
		if t, ok := child.(*goldast.Text); ok {
			parts = append(parts, string(t.Segment.Value(source)))
		}
		return goldast.WalkContinue, nil
	})
	return strings.Join(parts, "")
}

func goldmarkCodeContent(n goldast.Node, source []byte) string {
	lines := n.Lines()
	var sb strings.Builder
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		sb.Write(line.Value(source))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func goldmarkListToBlock(list *goldast.List, source []byte) runtime.Dict {
	entries := map[string]runtime.DictEntry{}

	isTaskBlock := func(n goldast.Node) bool {
		return n.Kind() == goldast.KindParagraph || n.Kind() == goldast.KindTextBlock
	}

	isTask := false
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		if item.Kind() == goldast.KindListItem {
			for para := item.FirstChild(); para != nil; para = para.NextSibling() {
				if isTaskBlock(para) && para.FirstChild() != nil {
					if para.FirstChild().Kind() == extast.KindTaskCheckBox {
						isTask = true
						break
					}
				}
			}
		}
		if isTask {
			break
		}
	}

	if isTask {
		putMarkdownDict(entries, "type", runtime.String{Value: "task_list"})
		var items []runtime.Value
		for item := list.FirstChild(); item != nil; item = item.NextSibling() {
			if item.Kind() != goldast.KindListItem {
				continue
			}
			checked := false
			for para := item.FirstChild(); para != nil; para = para.NextSibling() {
				if isTaskBlock(para) && para.FirstChild() != nil {
					if cb, ok := para.FirstChild().(*extast.TaskCheckBox); ok {
						checked = cb.IsChecked
					}
				}
			}
			text := strings.TrimSpace(goldmarkNodeText(item, source))
			itemEntries := map[string]runtime.DictEntry{}
			putMarkdownDict(itemEntries, "text", runtime.String{Value: text})
			putMarkdownDict(itemEntries, "checked", runtime.Bool{Value: checked})
			items = append(items, runtime.Dict{Entries: itemEntries})
		}
		if items == nil {
			items = []runtime.Value{}
		}
		putMarkdownDict(entries, "items", &runtime.List{Elements: items})
	} else if list.IsOrdered() {
		putMarkdownDict(entries, "type", runtime.String{Value: "ordered_list"})
		var items []runtime.Value
		for item := list.FirstChild(); item != nil; item = item.NextSibling() {
			if item.Kind() != goldast.KindListItem {
				continue
			}
			items = append(items, runtime.String{Value: strings.TrimSpace(goldmarkNodeText(item, source))})
		}
		if items == nil {
			items = []runtime.Value{}
		}
		putMarkdownDict(entries, "items", &runtime.List{Elements: items})
	} else {
		putMarkdownDict(entries, "type", runtime.String{Value: "list"})
		var items []runtime.Value
		for item := list.FirstChild(); item != nil; item = item.NextSibling() {
			if item.Kind() != goldast.KindListItem {
				continue
			}
			items = append(items, runtime.String{Value: strings.TrimSpace(goldmarkNodeText(item, source))})
		}
		if items == nil {
			items = []runtime.Value{}
		}
		putMarkdownDict(entries, "items", &runtime.List{Elements: items})
	}

	return runtime.Dict{Entries: entries}
}

func goldmarkTableToBlock(n goldast.Node, source []byte) runtime.Dict {
	entries := map[string]runtime.DictEntry{}
	putMarkdownDict(entries, "type", runtime.String{Value: "table"})

	var headers []runtime.Value
	var rows []runtime.Value

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch child.Kind() {
		case extast.KindTableHeader:
			for cell := child.FirstChild(); cell != nil; cell = cell.NextSibling() {
				if cell.Kind() == extast.KindTableCell {
					headers = append(headers, runtime.String{Value: strings.TrimSpace(goldmarkNodeText(cell, source))})
				}
			}
		case extast.KindTableRow:
			var cells []runtime.Value
			for cell := child.FirstChild(); cell != nil; cell = cell.NextSibling() {
				if cell.Kind() == extast.KindTableCell {
					cells = append(cells, runtime.String{Value: strings.TrimSpace(goldmarkNodeText(cell, source))})
				}
			}
			if cells == nil {
				cells = []runtime.Value{}
			}
			rows = append(rows, &runtime.List{Elements: cells})
		}
	}

	if headers == nil {
		headers = []runtime.Value{}
	}
	if rows == nil {
		rows = []runtime.Value{}
	}

	putMarkdownDict(entries, "headers", &runtime.List{Elements: headers})
	putMarkdownDict(entries, "rows", &runtime.List{Elements: rows})
	return runtime.Dict{Entries: entries}
}

func registerTemplate(r *Registry) {
	r.Register("template", "renderString", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("template.renderString expects template text and data")
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("template.renderString template text must be string")
		}
		rendered, err := renderTemplateText("inline", text.Value, args[1])
		if err != nil {
			return nil, fmt.Errorf("template.renderString: %v", err)
		}
		return runtime.String{Value: rendered}, nil
	})
	r.Register("template", "Template", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("template.Template expects text and optional name")
		}
		text, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("template.Template text must be string")
		}
		name := "inline"
		if len(args) == 2 {
			nameValue, ok := args[1].(runtime.String)
			if !ok {
				return nil, fmt.Errorf("template.Template name must be string")
			}
			name = nameValue.Value
		}
		return runtime.TemplateValue{Name: name, Text: text.Value}, nil
	})
	r.Register("template", "load", func(args []runtime.Value) (runtime.Value, error) {
		path, err := singleString(args, "template.load")
		if err != nil {
			return nil, err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("template.load: %v", err)
		}
		return runtime.TemplateValue{Name: filepath.Base(path), Text: string(text), Path: path}, nil
	})
	r.Register("template", "Engine", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) > 1 {
			return nil, fmt.Errorf("template.Engine expects optional directory or options dict")
		}
		dir := "templates"
		if len(args) == 1 {
			switch value := args[0].(type) {
			case runtime.String:
				dir = value.Value
			case runtime.Dict:
				if configured := dictString(value, "dir"); configured != "" {
					dir = configured
				} else if configured := dictString(value, "templates"); configured != "" {
					dir = configured
				}
			default:
				return nil, fmt.Errorf("template.Engine expects string or dict")
			}
		}
		return runtime.TemplateEngine{Dir: dir}, nil
	})
}

func TemplateMethod(receiver runtime.TemplateValue, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "name":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Template.name expects no arguments")
		}
		return runtime.String{Value: receiver.Name}, nil
	case "path":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Template.path expects no arguments")
		}
		if receiver.Path == "" {
			return runtime.Null{}, nil
		}
		return runtime.String{Value: receiver.Path}, nil
	case "render":
		if len(args) != 1 {
			return nil, fmt.Errorf("template.Template.render expects data")
		}
		rendered, err := renderTemplateText(receiver.Name, receiver.Text, args[0])
		if err != nil {
			return nil, fmt.Errorf("template.Template.render: %v", err)
		}
		return runtime.String{Value: rendered}, nil
	case "toString":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Template.toString expects no arguments")
		}
		return runtime.String{Value: receiver.Text}, nil
	default:
		return nil, fmt.Errorf("template.Template has no method %s", name)
	}
}

func TemplateEngineMethod(receiver runtime.TemplateEngine, name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "dir":
		if len(args) != 0 {
			return nil, fmt.Errorf("template.Engine.dir expects no arguments")
		}
		return runtime.String{Value: receiver.Dir}, nil
	case "load":
		path, err := singleString(args, "template.Engine.load")
		if err != nil {
			return nil, err
		}
		fullPath, err := templatePath(receiver.Dir, path)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.load: %v", err)
		}
		text, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.load: %v", err)
		}
		return runtime.TemplateValue{Name: path, Text: string(text), Path: fullPath}, nil
	case "render":
		if len(args) != 2 {
			return nil, fmt.Errorf("template.Engine.render expects name and data")
		}
		name, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("template.Engine.render name must be string")
		}
		fullPath, err := templatePath(receiver.Dir, name.Value)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.render: %v", err)
		}
		text, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("template.Engine.render: %v", err)
		}
		rendered, err := renderTemplateText(name.Value, string(text), args[1])
		if err != nil {
			return nil, fmt.Errorf("template.Engine.render: %v", err)
		}
		return runtime.String{Value: rendered}, nil
	default:
		return nil, fmt.Errorf("template.Engine has no method %s", name)
	}
}

func renderTemplateText(name string, text string, data runtime.Value) (string, error) {
	tmpl, err := htmltemplate.New(name).Parse(text)
	if err != nil {
		return "", err
	}
	goData, err := ValueToTemplateData(data)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, goData); err != nil {
		return "", err
	}
	return out.String(), nil
}

func templatePath(dir string, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("template name is required")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("template name must be relative")
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." || strings.HasPrefix(cleanName, ".."+string(os.PathSeparator)) || cleanName == ".." {
		return "", fmt.Errorf("template name escapes template directory")
	}
	return filepath.Join(dir, cleanName), nil
}

// singleHashInput accepts either a string or bytes value and returns
// the raw bytes. Used by every crypt hash so binary content can be
// hashed without round-tripping through hex.
func singleHashInput(args []runtime.Value, label string) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", label)
	}
	switch v := args[0].(type) {
	case runtime.String:
		return []byte(v.Value), nil
	case runtime.Bytes:
		return v.Value, nil
	}
	return nil, fmt.Errorf("%s expects a string or bytes argument", label)
}

// hmacInputs accepts (key, message) pairs where either side may be
// string or bytes. Used by hmacSha256 + hmacSha256Bytes.
func hmacInputs(args []runtime.Value, label string) ([]byte, []byte, error) {
	if len(args) != 2 {
		return nil, nil, fmt.Errorf("%s expects exactly two arguments", label)
	}
	key, err := bytesLike(args[0], label+" key")
	if err != nil {
		return nil, nil, err
	}
	msg, err := bytesLike(args[1], label+" message")
	if err != nil {
		return nil, nil, err
	}
	return key, msg, nil
}

func bytesLike(v runtime.Value, label string) ([]byte, error) {
	switch x := v.(type) {
	case runtime.String:
		return []byte(x.Value), nil
	case runtime.Bytes:
		return x.Value, nil
	}
	return nil, fmt.Errorf("%s must be string or bytes", label)
}

func singleString(args []runtime.Value, label string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects exactly one argument", label)
	}
	value, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s expects a string argument", label)
	}
	return value.Value, nil
}

// parseAsArgs validates the two-argument shape shared by
// json.parseAs/yaml.parseAs/toml.parseAs/xml.parseAs:
// (text: string, target: class).
func parseAsArgs(args []runtime.Value, label string) (string, runtime.Value, error) {
	if len(args) != 2 {
		return "", nil, fmt.Errorf("%s expects (text, class)", label)
	}
	text, ok := args[0].(runtime.String)
	if !ok {
		return "", nil, fmt.Errorf("%s expects a string for the first argument", label)
	}
	if !isClassValue(args[1]) {
		return "", nil, fmt.Errorf("%s expects a class for the second argument", label)
	}
	return text.Value, args[1], nil
}

// isClassValue reports whether the value is a class reference
// (either evaluator's *runtime.Class, the VM's BytecodeClass, or a
// DecoratorTarget with Target=="class" - the compile-time precomputed
// form the VM hands back from `reflect.class("Name")` for a literal
// name argument).
func isClassValue(value runtime.Value) bool {
	switch v := value.(type) {
	case *runtime.Class:
		return true
	case runtime.BytecodeClass:
		return true
	case runtime.DecoratorTarget:
		return v.Target == "class"
	}
	return false
}

// deserializeIntoClass delegates to the active backend's
// ClassDeserializer. Returns an error when no backend has
// registered one.
func deserializeIntoClass(class runtime.Value, value runtime.Value) (runtime.Value, error) {
	fn := GetClassDeserializer()
	if fn == nil {
		return nil, fmt.Errorf("class deserialization is unavailable: no active interpreter")
	}
	return fn(class, value)
}

func singleBytes(args []runtime.Value, label string) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s expects exactly one argument", label)
	}
	value, ok := args[0].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s expects a bytes argument", label)
	}
	return value.Value, nil
}

func singleInt64(args []runtime.Value, label string) (int64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s expects exactly one argument", label)
	}
	n, ok := AsInt64(args[0])
	if !ok {
		return 0, fmt.Errorf("%s expects an integer argument", label)
	}
	return n, nil
}

func stringWithOptionalUTF8Encoding(args []runtime.Value, label string) (string, error) {
	if len(args) != 1 && len(args) != 2 {
		return "", fmt.Errorf("%s expects text and optional encoding", label)
	}
	text, ok := args[0].(runtime.String)
	if !ok {
		return "", fmt.Errorf("%s text must be string", label)
	}
	if len(args) == 2 {
		encoding, ok := args[1].(runtime.String)
		if !ok || !strings.EqualFold(encoding.Value, "utf-8") {
			return "", fmt.Errorf("%s only supports utf-8 encoding", label)
		}
	}
	return text.Value, nil
}

func bytesWithOptionalUTF8Encoding(args []runtime.Value, label string) ([]byte, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("%s expects bytes and optional encoding", label)
	}
	data, ok := args[0].(runtime.Bytes)
	if !ok {
		return nil, fmt.Errorf("%s data must be bytes", label)
	}
	if len(args) == 2 {
		encoding, ok := args[1].(runtime.String)
		if !ok || !strings.EqualFold(encoding.Value, "utf-8") {
			return nil, fmt.Errorf("%s only supports utf-8 encoding", label)
		}
	}
	return data.Value, nil
}

func secureRandomBytes(args []runtime.Value, label string) ([]byte, error) {
	size, err := singleInt64(args, label)
	if err != nil {
		return nil, err
	}
	if size < 0 || size > 1<<20 {
		return nil, fmt.Errorf("%s byte count out of range", label)
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

func constantTimeEqual(left []byte, right []byte) bool {
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

func registerSys(r *Registry) {
	r.Register("sys", "hostname", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.hostname expects no arguments")
		}
		name, err := sysHostname()
		if err != nil {
			return nil, fmt.Errorf("sys.hostname: %v", err)
		}
		return runtime.String{Value: name}, nil
	})
	r.Register("sys", "pid", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.pid expects no arguments")
		}
		return runtime.NewInt64(int64(sysPid())), nil
	})
	r.Register("sys", "goroutineId", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.goroutineId expects no arguments")
		}
		return runtime.NewInt64(sysGoroutineID()), nil
	})
	r.Register("sys", "platform", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.platform expects no arguments")
		}
		return runtime.String{Value: sysPlatform()}, nil
	})
	r.Register("sys", "arch", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.arch expects no arguments")
		}
		return runtime.String{Value: sysArch()}, nil
	})
	r.Register("sys", "tmpdir", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.tmpdir expects no arguments")
		}
		return runtime.String{Value: sysTmpDir()}, nil
	})
	r.Register("sys", "homedir", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.homedir expects no arguments")
		}
		dir, err := sysHomeDir()
		if err != nil {
			return nil, fmt.Errorf("sys.homedir: %v", err)
		}
		return runtime.String{Value: dir}, nil
	})
	r.Register("sys", "username", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.username expects no arguments")
		}
		name, err := sysUsername()
		if err != nil {
			return nil, fmt.Errorf("sys.username: %v", err)
		}
		return runtime.String{Value: name}, nil
	})
	r.Register("sys", "environ", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("sys.environ expects no arguments")
		}
		entries := map[string]runtime.DictEntry{}
		for _, kv := range sysEnviron() {
			eq := strings.IndexByte(kv, '=')
			var k, v string
			if eq < 0 {
				k = kv
			} else {
				k = kv[:eq]
				v = kv[eq+1:]
			}
			keyValue := runtime.String{Value: k}
			entries[DictKey(keyValue)] = runtime.DictEntry{Key: keyValue, Value: runtime.String{Value: v}}
		}
		return runtime.Dict{Entries: entries}, nil
	})
}

func registerArgs(r *Registry) {
	r.Register("args", "parse", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("args.parse expects argv list and schema dict")
		}
		argv, ok := args[0].(*runtime.List)
		if !ok {
			return nil, fmt.Errorf("args.parse first argument must be a list")
		}
		schema, ok := args[1].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("args.parse second argument must be a dict")
		}
		return ParseArgv(argv, schema), nil
	})
	r.Register("args", "help", func(args []runtime.Value) (runtime.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("args.help expects program name and schema dict")
		}
		name, ok := args[0].(runtime.String)
		if !ok {
			return nil, fmt.Errorf("args.help first argument must be a string")
		}
		schema, ok := args[1].(runtime.Dict)
		if !ok {
			return nil, fmt.Errorf("args.help second argument must be a dict")
		}
		return runtime.String{Value: HelpText(name.Value, schema)}, nil
	})
}

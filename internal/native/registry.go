package native

import (
	"fmt"

	"geblang/internal/runtime"
)

type Function func(args []runtime.Value) (runtime.Value, error)

type Registry struct {
	functions map[string]Function
}

func NewRegistry() *Registry {
	return &Registry{functions: map[string]Function{}}
}

func Key(module, name string) string {
	return module + "." + name
}

func (r *Registry) Register(module, name string, fn Function) {
	r.functions[Key(module, name)] = fn
}

func (r *Registry) Call(module, name string, args []runtime.Value) (runtime.Value, error) {
	fn, ok := r.functions[Key(module, name)]
	if !ok {
		return nil, fmt.Errorf("unsupported native call %s.%s", module, name)
	}
	return fn(args)
}

func (r *Registry) Has(module, name string) bool {
	_, ok := r.functions[Key(module, name)]
	return ok
}

func (r *Registry) CallKey(key string, args []runtime.Value) (runtime.Value, error) {
	fn, ok := r.functions[key]
	if !ok {
		return nil, fmt.Errorf("unsupported native call %s", key)
	}
	return fn(args)
}

func IsPureBuiltin(module, name string) bool {
	functions, ok := pureBuiltins[module]
	if !ok {
		return false
	}
	_, ok = functions[name]
	return ok
}

func IsPureBuiltinModule(module string) bool {
	_, ok := pureBuiltins[module]
	return ok
}

var pureBuiltins = map[string]map[string]struct{}{
	"math": {
		"abs": {}, "min": {}, "max": {}, "clamp": {}, "floor": {}, "ceil": {},
		"round": {}, "sqrt": {}, "sin": {}, "cos": {}, "tan": {}, "asin": {},
		"acos": {}, "atan": {}, "atan2": {}, "log": {}, "log10": {}, "exp": {},
		"pow": {}, "pi": {}, "e": {},
		"log2": {}, "trunc": {}, "sign": {}, "cbrt": {}, "hypot": {},
		"inf": {}, "nan": {}, "isNaN": {}, "isInf": {},
		"median": {}, "percentile": {}, "quantile": {}, "mode": {},
	},
	"sys": {
		"hostname": {}, "pid": {}, "platform": {}, "arch": {},
		"tmpdir": {}, "homedir": {}, "username": {}, "environ": {},
	},
	"json": {
		"parse": {}, "stringify": {}, "validate": {}, "tryParse": {},
		"validateDetailed": {}, "parseAs": {},
	},
	"xml": {
		"parse": {}, "tryParse": {}, "stringify": {}, "validate": {},
		"validateDetailed": {}, "parseAs": {},
	},
	"toml": {
		"parse": {}, "tryParse": {}, "stringify": {}, "validate": {},
		"validateDetailed": {}, "parseAs": {},
	},
	"yaml": {
		"parse": {}, "tryParse": {}, "stringify": {}, "validate": {},
		"validateDetailed": {}, "parseAs": {},
	},
	"crypt": {
		"md5": {}, "sha1": {}, "sha256": {}, "sha512": {}, "sha3_256": {},
		"blake2b": {}, "crc32": {}, "hmacSha256": {}, "randomHex": {},
		"bcryptHash": {}, "bcryptVerify": {}, "argon2idHash": {}, "argon2idVerify": {},
		"base64Encode": {}, "base64Decode": {},
		"jwtSign": {}, "jwtVerify": {}, "jwtDecode": {},
		"generateRsaKey": {}, "generateEcKey": {}, "generateEd25519Key": {},
		"publicKey": {}, "generateSelfSignedCert": {}, "generateCsr": {}, "parseCert": {},
		"jwtSignRS256": {}, "jwtVerifyRS256": {}, "jwtSignES256": {}, "jwtVerifyES256": {},
		"aesEncrypt": {}, "aesDecrypt": {},
		"chacha20Encrypt": {}, "chacha20Decrypt": {},
	},
	"datetime": {
		"nowUnix": {}, "unix": {}, "parse": {}, "format": {}, "addSeconds": {},
		"addDays": {}, "addMonths": {}, "addYears": {}, "diff": {},
		"toLocal": {}, "toUtc": {}, "now": {},
		"nowInstant": {}, "Instant": {}, "Duration": {}, "Zone": {},
		"make": {}, "formatRFC3339": {}, "formatDate": {}, "formatTime": {},
		"formatHTTP": {}, "partsInZone": {},
		"parseRFC3339": {}, "weekdayName": {}, "monthName": {},
	},
	"random": {
		"seed": {}, "next": {}, "intRange": {}, "float": {}, "bool": {},
		"choice": {}, "shuffle": {}, "Generator": {},
	},
	"time": {
		"now": {}, "elapsed": {}, "sleep": {},
	},
	"collections": {
		"length": {}, "isEmpty": {}, "contains": {}, "reverse": {}, "sort": {}, "join": {},
		"map": {}, "filter": {}, "reduce": {}, "find": {}, "any": {}, "all": {},
		"flatten": {}, "unique": {}, "zip": {}, "sorted": {},
		"groupBy": {}, "chunk": {}, "partition": {},
		"findLast": {}, "containsBy": {}, "indexBy": {},
		"binarySearch": {}, "lowerBound": {}, "upperBound": {},
		"minBy": {}, "maxBy": {}, "sortBy": {}, "topBy": {}, "sumBy": {}, "averageBy": {},
		"topK": {}, "bottomK": {}, "frequencies": {}, "mode": {},
		"difference": {}, "intersection": {}, "differenceBy": {}, "intersectionBy": {}, "zipWith": {},
		"range": {}, "take": {}, "lazyMap": {}, "lazyFilter": {},
		"bfs": {}, "dfs": {}, "topologicalSort": {}, "shortestPath": {},
	},
	"profiler": {
		"snapshot": {}, "delta": {}, "memory": {}, "cpu": {}, "peak": {},
	},
	"secrets": {
		"randomBytes": {}, "randomInt": {}, "randomHex": {}, "randomBase64": {},
		"constantTimeEqual": {},
	},
	"bytes": {
		"fromString": {}, "toString": {}, "fromHex": {}, "toHex": {},
		"fromBase64": {}, "toBase64": {}, "concat": {},
	},
	"string": {
		"fromCodePoint": {}, "fromCodePoints": {},
		"compare": {}, "equalsFold": {},
	},
	"strbuilder": {
		"new": {}, "append": {}, "appendLine": {}, "build": {},
		"length": {}, "clear": {}, "dispose": {},
	},
	"csv": {
		"parse": {}, "parseDict": {}, "stringify": {},
	},
	"compress": {
		"gzip": {}, "gunzip": {},
	},
	"encoding": {
		"base64Encode": {}, "base64Decode": {},
		"base32Encode": {}, "base32Decode": {},
		"base58Encode": {}, "base58Decode": {},
		"urlEncode": {}, "urlDecode": {},
		"htmlEscape": {}, "htmlUnescape": {},
	},
	"url": {
		"URL": {}, "parse": {}, "stringify": {}, "encode": {}, "decode": {}, "joinPath": {},
	},
	"template": {
		"renderString": {}, "Template": {}, "load": {}, "Engine": {},
	},
	"uuid": {
		"v1": {}, "v4": {}, "v7": {},
		"v3": {}, "v5": {},
		"parse": {}, "isValid": {}, "nil": {},
		"toBytes": {}, "fromBytes": {},
		"namespaceDNS": {}, "namespaceURL": {}, "namespaceOID": {}, "namespaceX500": {},
		"ulid": {},
	},
	"re": {
		"match": {}, "matchAll": {}, "find": {}, "findAll": {}, "replace": {}, "split": {}, "test": {},
	},
	"pcre": {
		"match": {}, "matchAll": {}, "find": {}, "findAll": {}, "replace": {}, "split": {}, "test": {}, "quote": {},
	},
	"markdown": {
		"parse": {}, "renderHtml": {}, "stripText": {},
	},
	"args": {
		"parse": {}, "help": {},
	},
	"errors": {
		"new": {}, "message": {}, "class": {}, "is": {}, "wrap": {},
		"stackTrace": {}, "frames": {}, "hasStackTrace": {},
	},
	"freeze": {
		"shallow": {}, "deep": {}, "isFrozen": {},
	},
}

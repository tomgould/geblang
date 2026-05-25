package lsp

import (
	"sort"
	"strings"
)

type moduleDoc struct {
	functions    map[string]functionDoc
	classes      map[string]string
	classMethods map[string]map[string]functionDoc
}

type functionDoc struct {
	name   string
	params []string
	result string
	doc    string
}

func fn(params []string, result, doc string) functionDoc {
	return functionDoc{params: params, result: result, doc: doc}
}

func (f functionDoc) signature() string {
	label := f.name + "(" + strings.Join(f.params, ", ") + ")"
	if f.result != "" {
		label += ": " + f.result
	}
	return label
}

func moduleNames() []string {
	names := make([]string, 0, len(stdlibCatalog))
	for name := range stdlibCatalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// lookupClassMethods returns the method table for a fully-qualified
// class name like "http.Request" or "db.Connection", or nil if the
// class is not catalogued.
func lookupClassMethods(qualified string) map[string]functionDoc {
	module, class, ok := splitQualifiedClass(qualified)
	if !ok {
		return nil
	}
	mod, ok := stdlibCatalog[module]
	if !ok {
		return nil
	}
	if mod.classMethods == nil {
		return nil
	}
	methods, ok := mod.classMethods[class]
	if !ok {
		return nil
	}
	return methods
}

// splitQualifiedClass parses "module.Class" -> ("module", "Class").
// Returns false for any other shape.
func splitQualifiedClass(qualified string) (string, string, bool) {
	idx := strings.IndexByte(qualified, '.')
	if idx <= 0 || idx == len(qualified)-1 {
		return "", "", false
	}
	module := qualified[:idx]
	class := qualified[idx+1:]
	if !isIdentLike(module) || !isIdentLike(class) {
		return "", "", false
	}
	return module, class, true
}

func isIdentLike(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && (r >= '0' && r <= '9') {
			return false
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

// testBaseMethods enumerates the assertion methods every class
// extending `test.Test` inherits from the builtin Test base. The
// list mirrors the assertions registered in
// internal/evaluator/evaluator.go installBuiltinTypes; keep them
// in sync. Surfaced by completion.go on `this.<TAB>` inside a
// test.Test subclass.
var testBaseMethods = map[string]functionDoc{
	"assertEquals":             fn([]string{"any expected", "any actual"}, "void", "Deep equality for primitives, lists, dicts, sets, enum variants, and object fields."),
	"assertEqual":              fn([]string{"any expected", "any actual"}, "void", "Alias for assertEquals."),
	"assertNotEquals":          fn([]string{"any expected", "any actual"}, "void", "Fails when values are deeply equal."),
	"assertNotEqual":           fn([]string{"any expected", "any actual"}, "void", "Alias for assertNotEquals."),
	"assertTrue":               fn([]string{"bool value"}, "void", "Fails unless value is true."),
	"assertFalse":              fn([]string{"bool value"}, "void", "Fails unless value is false."),
	"assertNull":               fn([]string{"any value"}, "void", "Fails unless value is null."),
	"assertNotNull":            fn([]string{"any value"}, "void", "Fails when value is null."),
	"assertContains":           fn([]string{"any haystack", "any needle"}, "void", "String substring, bytes byte/subsequence, list item, dict key, or set member."),
	"assertNotContains":        fn([]string{"any haystack", "any needle"}, "void", "Inverse of assertContains."),
	"assertEmpty":              fn([]string{"any value"}, "void", "Empty string, bytes, list, dict, set, range, or null."),
	"assertNotEmpty":           fn([]string{"any value"}, "void", "Inverse of assertEmpty."),
	"assertGreaterThan":        fn([]string{"any expected", "any actual"}, "void", "Ordered numeric or string comparison."),
	"assertGreaterThanOrEqual": fn([]string{"any expected", "any actual"}, "void", "Ordered numeric or string comparison."),
	"assertLessThan":           fn([]string{"any expected", "any actual"}, "void", "Ordered numeric or string comparison."),
	"assertLessThanOrEqual":    fn([]string{"any expected", "any actual"}, "void", "Ordered numeric or string comparison."),
	"assertThrows":             fn([]string{"callable fn", "string expectedSubstring = \"\""}, "void", "Fails unless the no-arg callable raises. Optional substring must appear in the error message."),
	"fail":                     fn([]string{"string message = \"\""}, "void", "Fails immediately with an optional message."),
}

// primitiveMethods enumerates the instance methods the LSP surfaces for
// each primitive type. The list is hand-maintained but tracks
// internal/bytecode/vm.go primitiveMethod (and the per-type method
// tables on List/Dict/Set/Bytes elsewhere in vm.go). When a method
// name is shared across types it is listed under every applicable
// receiver so autocomplete works regardless of the variable's type.
//
// Keys here match what fileVarTypes records: the type-keyword the
// user wrote in the declaration (`string`, `int`, etc.).
var primitiveMethods = map[string]map[string]functionDoc{
	"string": {
		"length":       fn([]string{}, "int", "Number of Unicode characters."),
		"isEmpty":      fn([]string{}, "bool", "True when the string is the empty string."),
		"contains":     fn([]string{"string needle"}, "bool", "Substring check."),
		"indexOf":      fn([]string{"string needle"}, "int", "Index of the first match, or -1."),
		"lastIndexOf":  fn([]string{"string needle"}, "int", "Index of the last match, or -1."),
		"startsWith":   fn([]string{"string prefix"}, "bool", "Prefix check."),
		"endsWith":     fn([]string{"string suffix"}, "bool", "Suffix check."),
		"substring":    fn([]string{"int start", "int end"}, "string", "Substring by code-point range."),
		"slice":        fn([]string{"int start", "int end"}, "string", "Alias for substring."),
		"split":        fn([]string{"string separator"}, "list<string>", "Split by separator."),
		"replace":      fn([]string{"string from", "string to"}, "string", "Replace all occurrences."),
		"splitRegex":   fn([]string{"string pattern"}, "list<string>", "Split by a regex pattern."),
		"replaceRegex": fn([]string{"string pattern", "string replacement"}, "string", "Replace all regex matches; $1 / $2 capture-group references in replacement supported."),
		"matchesRegex": fn([]string{"string pattern"}, "bool", "Test whether the string contains a regex match."),
		"trim":         fn([]string{}, "string", "Remove surrounding whitespace."),
		"trimStart":    fn([]string{}, "string", "Remove leading whitespace."),
		"trimEnd":      fn([]string{}, "string", "Remove trailing whitespace."),
		"padStart":     fn([]string{"int width", "string fill = \" \""}, "string", "Left-pad to a width."),
		"padEnd":       fn([]string{"int width", "string fill = \" \""}, "string", "Right-pad to a width."),
		"repeat":       fn([]string{"int n"}, "string", "Concatenate N copies."),
		"lower":        fn([]string{}, "string", "Lowercase."),
		"upper":        fn([]string{}, "string", "Uppercase."),
		"chars":        fn([]string{}, "list<string>", "List of single-character substrings."),
		"get":          fn([]string{"int index"}, "string", "Character at position."),
		"codepointAt":  fn([]string{"int index"}, "int", "Code point at position."),
		"format":       fn([]string{"any ...args"}, "string", "printf-style format using % placeholders."),
		"toString":     fn([]string{}, "string", "Identity."),
		"toInt":        fn([]string{}, "int", "Parse as int."),
		"toFloat":      fn([]string{}, "float", "Parse as float."),
		"toDecimal":    fn([]string{}, "decimal", "Parse as decimal."),
		"toBool":       fn([]string{}, "bool", "Parse as bool (true/false)."),
	},
	"int": {
		"abs":         fn([]string{}, "int", "Absolute value."),
		"isPositive":  fn([]string{}, "bool", "True if > 0."),
		"isNegative":  fn([]string{}, "bool", "True if < 0."),
		"isZero":      fn([]string{}, "bool", "True if == 0."),
		"toHex":       fn([]string{}, "string", "Lowercase hex representation."),
		"toString":    fn([]string{}, "string", "Decimal string."),
		"toFloat":     fn([]string{}, "float", "Convert to float."),
		"toDecimal":   fn([]string{}, "decimal", "Convert to decimal."),
		"toBool":      fn([]string{}, "bool", "Non-zero -> true."),
	},
	"float": {
		"abs":        fn([]string{}, "float", "Absolute value."),
		"isPositive": fn([]string{}, "bool", "True if > 0."),
		"isNegative": fn([]string{}, "bool", "True if < 0."),
		"isZero":     fn([]string{}, "bool", "True if == 0."),
		"isNaN":      fn([]string{}, "bool", "True if NaN."),
		"isInf":      fn([]string{}, "bool", "True if infinite."),
		"toString":   fn([]string{}, "string", "Decimal string."),
		"toInt":      fn([]string{}, "int", "Truncate to int."),
		"toDecimal":  fn([]string{}, "decimal", "Convert to decimal."),
		"toBool":     fn([]string{}, "bool", "Non-zero -> true."),
	},
	"decimal": {
		"abs":        fn([]string{}, "decimal", "Absolute value."),
		"isPositive": fn([]string{}, "bool", "True if > 0."),
		"isNegative": fn([]string{}, "bool", "True if < 0."),
		"isZero":     fn([]string{}, "bool", "True if == 0."),
		"format":     fn([]string{"int scale"}, "string", "Fixed-scale string formatting."),
		"toString":   fn([]string{}, "string", "Default decimal-string formatting."),
		"toInt":      fn([]string{}, "int", "Truncate to int."),
		"toFloat":    fn([]string{}, "float", "Convert to float (may lose precision)."),
		"toBool":     fn([]string{}, "bool", "Non-zero -> true."),
	},
	"bool": {
		"not":      fn([]string{}, "bool", "Logical negation."),
		"toString": fn([]string{}, "string", "\"true\" or \"false\"."),
		"toInt":    fn([]string{}, "int", "true -> 1, false -> 0."),
		"toBool":   fn([]string{}, "bool", "Identity."),
	},
	"bytes": {
		"length":   fn([]string{}, "int", "Number of bytes."),
		"isEmpty":  fn([]string{}, "bool", "True when length is zero."),
		"contains": fn([]string{"bytes|int needle"}, "bool", "Substring or byte-value check."),
		"toString": fn([]string{}, "string", "Decode as UTF-8."),
		"toHex":    fn([]string{}, "string", "Lowercase hex."),
		"get":      fn([]string{"int index"}, "int", "Byte value at position (0-255)."),
	},
	"list": {
		"length":      fn([]string{}, "int", "Number of elements."),
		"isEmpty":     fn([]string{}, "bool", "True when length is zero."),
		"contains":    fn([]string{"any value"}, "bool", "Linear-scan membership check."),
		"push":        fn([]string{"any value"}, "list<any>", "Returns a new list with value appended."),
		"pop":         fn([]string{}, "list<any>", "Returns a new list with the last element removed."),
		"get":         fn([]string{"int index"}, "any", "Element at position."),
		"set":         fn([]string{"int index", "any value"}, "list<any>", "Returns a new list with index updated."),
		"indexOf":     fn([]string{"any value"}, "int", "First index of value, or -1."),
		"lastIndexOf": fn([]string{"any value"}, "int", "Last index of value, or -1."),
		"slice":       fn([]string{"int start", "int end"}, "list<any>", "Sub-list."),
		"reverse":     fn([]string{}, "list<any>", "Returns a reversed copy."),
		"reversed":    fn([]string{}, "list<any>", "Alias for reverse."),
		"sort":        fn([]string{}, "list<any>", "Returns a sorted copy (natural order)."),
		"sorted":      fn([]string{}, "list<any>", "Alias for sort."),
		"map":         fn([]string{"func fn"}, "list<any>", "Apply fn to each element."),
		"filter":      fn([]string{"func predicate"}, "list<any>", "Keep elements matching predicate."),
		"reduce":      fn([]string{"any initial", "func fn"}, "any", "Left fold."),
		"find":        fn([]string{"func predicate"}, "any", "First element matching predicate, or null."),
		"any":         fn([]string{"func predicate"}, "bool", "True if any element matches."),
		"all":         fn([]string{"func predicate"}, "bool", "True if every element matches."),
		"count":       fn([]string{"func predicate"}, "int", "Number of elements matching predicate."),
		"join":        fn([]string{"string separator"}, "string", "Concatenate as strings."),
		"first":       fn([]string{}, "any", "First element or null."),
		"last":        fn([]string{}, "any", "Last element or null."),
	},
	"dict": {
		"length":  fn([]string{}, "int", "Number of entries."),
		"isEmpty": fn([]string{}, "bool", "True when length is zero."),
		"hasKey":  fn([]string{"any key"}, "bool", "Membership check on keys."),
		"get":     fn([]string{"any key"}, "any", "Value or null."),
		"set":     fn([]string{"any key", "any value"}, "dict<any, any>", "Returns a new dict with key set."),
		"delete":  fn([]string{"any key"}, "dict<any, any>", "Returns a new dict without the key."),
		"keys":    fn([]string{}, "list<any>", "All keys."),
		"values":  fn([]string{}, "list<any>", "All values."),
		"items":   fn([]string{}, "list<list<any>>", "All [key, value] pairs."),
		"merge":   fn([]string{"dict<any, any> other"}, "dict<any, any>", "Right-biased merge."),
	},
	"set": {
		"length":       fn([]string{}, "int", "Number of elements."),
		"isEmpty":      fn([]string{}, "bool", "True when length is zero."),
		"contains":     fn([]string{"any value"}, "bool", "Membership check."),
		"add":          fn([]string{"any value"}, "set<any>", "Returns a new set with value added."),
		"remove":       fn([]string{"any value"}, "set<any>", "Returns a new set without value."),
		"union":        fn([]string{"set<any> other"}, "set<any>", "Union."),
		"intersection": fn([]string{"set<any> other"}, "set<any>", "Intersection."),
		"difference":   fn([]string{"set<any> other"}, "set<any>", "Asymmetric difference."),
	},
	"range": {
		"length":   fn([]string{}, "int", "Number of values in the range."),
		"start":    fn([]string{}, "int", "Inclusive start."),
		"end":      fn([]string{}, "int", "Exclusive end."),
		"step":     fn([]string{}, "int", "Step value (default 1)."),
		"contains": fn([]string{"int value"}, "bool", "Membership check."),
		"toList":   fn([]string{}, "list<int>", "Materialise as a list."),
	},
}

// primitiveTypeNames are the type keywords primitiveMethods is keyed
// by. Used by fileVarTypes when scanning a source file for typed
// declarations.
var primitiveTypeNames = map[string]struct{}{
	"string": {}, "int": {}, "float": {}, "decimal": {}, "bool": {},
	"bytes": {}, "list": {}, "dict": {}, "set": {}, "range": {},
}

func init() {
	for _, mod := range stdlibCatalog {
		for name, doc := range mod.functions {
			doc.name = name
			mod.functions[name] = doc
		}
	}
}

var stdlibCatalog = map[string]moduleDoc{
	"args": {functions: map[string]functionDoc{
		"parse": fn([]string{"list<string> argv", "dict<string, any> spec"}, "dict<string, any>", "Parses command-line options."),
		"help":  fn([]string{"dict<string, any> spec"}, "string", "Builds usage text for an option specification."),
	}},
	"amqp": {functions: map[string]functionDoc{
		"dial":         fn([]string{"string url"}, "int", "Opens an AMQP 0.9.1 connection. Returns a connection handle. Prefer messaging.connect({driver: \"rabbitmq\", ...}) for the high-level API."),
		"channel":      fn([]string{"int connection"}, "int", "Opens a channel on a connection. Returns a channel handle."),
		"declareQueue": fn([]string{"int channel", "string name", "dict<string, any> opts = {}"}, "dict<string, any>", "Declares a queue (durable by default). Returns {name, messages, consumers}."),
		"publish":      fn([]string{"int channel", "string exchange", "string routingKey", "any body", "dict<string, any> opts = {}"}, "void", "Publishes a message. opts: contentType, persistent (default true)."),
		"get":          fn([]string{"int channel", "string queue", "bool autoAck = false"}, "?dict<string, any>", "Non-blocking get; returns null if no message. The dict carries body, deliveryTag, contentType, routingKey, exchange."),
		"ack":          fn([]string{"int channel", "int deliveryTag"}, "void", "Acknowledges a delivery."),
		"close":        fn([]string{"int handle"}, "void", "Closes a connection or channel handle."),
	}},
	"kafka": {functions: map[string]functionDoc{
		"writer": fn([]string{"dict<string, any> opts"}, "int", "Creates a Kafka producer. opts.brokers (list<string>) and opts.topic required. Returns a writer handle. Prefer messaging.connect({driver: \"kafka\", ...}) for the high-level API."),
		"write":  fn([]string{"int writer", "any value", "any key = null"}, "void", "Produces a record to the writer's topic. value is string or bytes. Optional key partitions by hash."),
		"reader": fn([]string{"dict<string, any> opts"}, "int", "Creates a Kafka consumer. opts.brokers, opts.topic, and opts.groupId required. Returns a reader handle."),
		"read":   fn([]string{"int reader", "int timeoutMs = 30000"}, "?dict<string, any>", "Fetches the next record assigned to the consumer group, or null on timeout. The dict carries value, key, topic, partition, offset."),
		"commit": fn([]string{"int reader"}, "void", "Commits the offset of the last fetched record."),
		"close":  fn([]string{"int handle"}, "void", "Closes a writer or reader handle."),
	}},
	"metrics": {functions: map[string]functionDoc{
		"inc":          fn([]string{"string name", "any amount = 1", "dict<string, string> labels = {}"}, "float", "Increments a counter / gauge. When the metric was declared with labels, the labels arg picks the per-combo slot."),
		"set":          fn([]string{"string name", "any value", "dict<string, string> labels = {}"}, "float", "Sets a counter / gauge to value. When the metric was declared with labels, the labels arg picks the per-combo slot."),
		"get":          fn([]string{"string name"}, "float", "Reads the current value of a label-less counter / gauge."),
		"snapshot":     fn([]string{}, "dict<string, float>", "Returns a snapshot of label-less metric values."),
		"reset":        fn([]string{"string name = \"\""}, "void", "Resets one metric (when name is given) or every metric (when omitted)."),
		"now":          fn([]string{}, "int", "Returns the current monotonic timestamp in nanoseconds."),
		"duration":     fn([]string{"int startNs"}, "int", "Returns nanoseconds elapsed since startNs."),
		"counter":      fn([]string{"string name", "dict<string, any> opts = {}"}, "string", "Declares a counter. opts.help (string), opts.labels (list<string>) for label dimensions."),
		"gauge":        fn([]string{"string name", "dict<string, any> opts = {}"}, "string", "Declares a gauge. opts.help (string), opts.labels (list<string>) for label dimensions."),
		"histogram":    fn([]string{"string name", "dict<string, any> opts = {}"}, "string", "Declares a histogram. opts.help (string), opts.labels (list<string>), opts.buckets (list<float> ascending upper bounds)."),
		"observe":      fn([]string{"string name", "any value", "dict<string, string> labels = {}"}, "void", "Records a histogram sample under the given label combination."),
		"toPrometheus": fn([]string{}, "string", "Emits every registered metric in Prometheus v0.0.4 text exposition format. Legacy label-less metrics appear as untyped."),
	}},
	"trace": {functions: map[string]functionDoc{
		"start":      fn([]string{"string name", "dict<string, any> attrs = {}", "dict<string, any> opts = {}"}, "int", "Starts a span. opts.parent (a previously returned span handle) makes the new span a child sharing the parent's traceId. Returns a span handle."),
		"event":      fn([]string{"int span", "string name", "dict<string, any> attrs = {}"}, "void", "Attaches an event to a span."),
		"end":        fn([]string{"int span"}, "dict<string, any>", "Finishes the span. Returns the span's full record."),
		"snapshot":   fn([]string{}, "list<dict<string, any>>", "Returns the full list of recorded spans."),
		"reset":      fn([]string{}, "void", "Clears every recorded span."),
		"toOtlpJson": fn([]string{"dict<string, any> opts = {}"}, "string", "Serializes recorded spans to an OTLP/HTTP JSON request body. opts may set serviceName, scopeName, scopeVersion, and resource (dict<string, string>)."),
		"exportOtlp": fn([]string{"string endpoint", "dict<string, any> opts = {}"}, "dict<string, any>", "POSTs every recorded span to an OTLP/HTTP collector at endpoint (e.g. http://localhost:4318); /v1/traces is appended automatically. opts may set serviceName, scopeName, scopeVersion, resource, headers, timeoutMs. Returns {status, ok}."),
	}},
	"async": {functions: map[string]functionDoc{
		"run":     fn([]string{"func task"}, "Task", "Starts asynchronous work."),
		"sleep":   fn([]string{"int milliseconds"}, "Task", "Completes after the requested delay."),
		"await":   fn([]string{"Task task"}, "any", "Waits for an async task result."),
		"done":    fn([]string{"Task task"}, "bool", "Reports whether a task has completed."),
		"all":     fn([]string{"list<Task> tasks"}, "Task", "Waits for every task; fails fast and cancels the rest on the first error."),
		"race":    fn([]string{"list<Task> tasks"}, "Task", "Returns the first task to complete; cancels the rest."),
		"timeout": fn([]string{"Task task", "int milliseconds"}, "Task", "Cancels the task if it does not finish within the deadline."),
		"cancel":  fn([]string{"Task task"}, "void", "Cancels a running task."),
		"token":   fn([]string{}, "Task", "Returns a fresh uncompleted Task used as a pure cancellation signal (used by Timer/Ticker)."),
	}},
	"bytes": {functions: map[string]functionDoc{
		"fromString": fn([]string{"string text"}, "bytes", "Encodes a string as bytes."),
		"toString":   fn([]string{"bytes data"}, "string", "Decodes bytes as a string."),
		"fromHex":    fn([]string{"string hex"}, "bytes", "Decodes hex text."),
		"toHex":      fn([]string{"bytes data"}, "string", "Encodes bytes as hex text."),
		"fromBase64": fn([]string{"string text"}, "bytes", "Decodes base64 text."),
		"toBase64":   fn([]string{"bytes data"}, "string", "Encodes bytes as base64 text."),
		"concat":     fn([]string{"bytes left", "bytes right"}, "bytes", "Concatenates byte buffers."),
	}},
	"cli": {functions: map[string]functionDoc{
		"prompt":         fn([]string{"string label", "string default = \"\""}, "string", "Reads interactive console input."),
		"password":       fn([]string{"string label"}, "string", "Reads masked console input."),
		"secret":         fn([]string{"string label"}, "string", "Reads masked console input."),
		"confirm":        fn([]string{"string label", "bool default = false"}, "bool", "Reads a yes/no answer."),
		"choose":         fn([]string{"string label", "list<string> choices"}, "string", "Reads a choice from a list."),
		"style":          fn([]string{"string text", "dict<string, any> options"}, "string", "Applies ANSI console styling."),
		"stripAnsi":      fn([]string{"string text"}, "string", "Removes ANSI escape sequences."),
		"table":          fn([]string{"list<dict<string, any>> rows"}, "string", "Formats rows as a console table."),
		"parseArgs":      fn([]string{"list<string> argv", "dict<string, any> spec"}, "dict<string, any>", "Parses command-line options."),
		"help":           fn([]string{"dict<string, any> spec"}, "string", "Builds usage text for an option specification."),
		"spinnerTick":    fn([]string{"int frameIndex", "string message"}, "int", "Renders one spinner frame to stderr; returns the next frame index (1.2.0). Use cli.widgets.Spinner for the OO wrapper."),
		"spinnerStop":    fn([]string{"string finalMessage = \"\""}, "void", "Clears the spinner line (1.2.0)."),
		"progressRender": fn([]string{"int current", "int total", "int width = 30", "string label = \"\""}, "void", "Renders an ANSI progress bar to stderr (1.2.0). Use cli.widgets.ProgressBar."),
		"progressFinish": fn([]string{"string finalMessage = \"\""}, "void", "Clears the progress line (1.2.0)."),
	}},
	"cli.widgets": {classes: map[string]string{
		"Spinner":     "Terminal spinner that renders to stderr (1.2.0). Construct with `Spinner(message)`; call `.tick()` periodically and `.stop(finalMessage?)` when done.",
		"ProgressBar": "Terminal progress bar that renders to stderr (1.2.0). Construct with `ProgressBar(total, width = 30, label = \"\")`; call `.advance(n = 1)`, `.set(value)`, `.updateLabel(label)`, or `.finish(finalMessage?)`.",
	}},
	"collections": {functions: map[string]functionDoc{
		"length":       fn([]string{"any value"}, "int", "Returns the size of a collection or string."),
		"isEmpty":      fn([]string{"any value"}, "bool", "Reports whether a collection is empty."),
		"contains":     fn([]string{"any collection", "any value"}, "bool", "Checks collection membership."),
		"reverse":      fn([]string{"list<any> values"}, "list<any>", "Returns values in reverse order."),
		"sort":         fn([]string{"list<any> values"}, "list<any>", "Returns sorted values."),
		"join":         fn([]string{"list<any> values", "string separator"}, "string", "Joins values into a string."),
		"range":        fn([]string{"int start", "int end", "int step = 1"}, "list<int>", "Builds an integer range."),
		"take":         fn([]string{"any iterable", "int count"}, "list<any>", "Takes the first values from an iterable."),
		"map":          fn([]string{"list<any> values", "func callback"}, "list<any>", "Maps values through a callback."),
		"filter":       fn([]string{"list<any> values", "func predicate"}, "list<any>", "Filters values by predicate."),
		"reduce":       fn([]string{"list<any> values", "func reducer", "any initial"}, "any", "Reduces values to one result."),
		"find":         fn([]string{"list<any> values", "func predicate"}, "any", "Returns the first matching value."),
		"findLast":     fn([]string{"list<any> values", "func predicate"}, "any", "Returns the last matching value."),
		"any":          fn([]string{"list<any> values", "func predicate"}, "bool", "Checks whether any value matches."),
		"all":          fn([]string{"list<any> values", "func predicate"}, "bool", "Checks whether all values match."),
		"groupBy":      fn([]string{"list<any> values", "func selector"}, "dict<any, list<any>>", "Groups values by selector."),
		"binarySearch": fn([]string{"list<any> values", "any target"}, "int", "Searches a sorted list."),
		"topK":         fn([]string{"list<any> values", "int count"}, "list<any>", "Returns the largest values."),
		"bfs":          fn([]string{"dict<any, list<any>> graph", "any start"}, "list<any>", "Breadth-first traversal."),
		"dfs":          fn([]string{"dict<any, list<any>> graph", "any start"}, "list<any>", "Depth-first traversal."),
	}},
	"db": {functions: map[string]functionDoc{
		"connect":   fn([]string{"string driver", "string dsn"}, "Connection", "Opens a database connection."),
		"open":      fn([]string{"string driver", "string dsn"}, "Connection", "Opens a database connection."),
		"exec":      fn([]string{"Connection conn", "string sql", "...any args"}, "Result", "Executes a statement."),
		"query":     fn([]string{"Connection conn", "string sql", "...any args"}, "list<dict<string, any>>", "Runs a query."),
		"prepare":   fn([]string{"Connection conn", "string sql"}, "Statement", "Prepares a reusable statement."),
		"begin":     fn([]string{"Connection conn"}, "Transaction", "Starts a transaction."),
		"commit":    fn([]string{"Transaction tx"}, "void", "Commits a transaction."),
		"rollback":  fn([]string{"Transaction tx"}, "void", "Rolls back a transaction."),
		"configure": fn([]string{"Connection conn", "dict<string, any> options"}, "void", "Configures connection pool options."),
		"stats":     fn([]string{"Connection conn"}, "dict<string, any>", "Returns connection statistics."),
		"close":     fn([]string{"Connection conn"}, "void", "Closes a database connection."),
	}, classes: map[string]string{
		"Connection":  "Database connection. Open via db.connect(driver, dsn) or db.Connection(...).",
		"Transaction": "In-flight SQL transaction. Returned by Connection.begin(); call .commit() or .rollback().",
		"Statement":   "Prepared SQL statement. Returned by Connection.prepare().",
		"Rows":        "Streamed query result. Iterate via .next() / .row() or call .all() to materialise.",
	}, classMethods: map[string]map[string]functionDoc{
		"Connection": {
			"exec":      fn([]string{"string sql", "...any args"}, "Result", "Executes a non-query statement."),
			"query":     fn([]string{"string sql", "...any args"}, "Rows", "Runs a query and returns a streaming Rows handle."),
			"begin":     fn([]string{}, "Transaction", "Starts a new transaction."),
			"prepare":   fn([]string{"string sql"}, "Statement", "Prepares a reusable statement."),
			"configure": fn([]string{"dict<string, any> options"}, "void", "Configures connection pool options (maxOpenConns, maxIdleConns, etc.)."),
			"stats":     fn([]string{}, "dict<string, any>", "Returns connection-pool statistics."),
			"migrate":   fn([]string{"list<string> statements"}, "void", "Applies the given DDL statements, tracking versions."),
			"close":     fn([]string{}, "void", "Closes the connection and releases pool resources."),
		},
		"Transaction": {
			"exec":     fn([]string{"string sql", "...any args"}, "Result", "Executes within the transaction."),
			"query":    fn([]string{"string sql", "...any args"}, "Rows", "Runs a query within the transaction."),
			"commit":   fn([]string{}, "void", "Commits the transaction."),
			"rollback": fn([]string{}, "void", "Rolls back the transaction."),
		},
		"Statement": {
			"exec":  fn([]string{"...any args"}, "Result", "Executes the prepared statement."),
			"query": fn([]string{"...any args"}, "Rows", "Runs the prepared statement as a query."),
			"close": fn([]string{}, "void", "Releases the statement."),
		},
		"Rows": {
			"next":    fn([]string{}, "bool", "Advances to the next row. Returns false past end."),
			"row":     fn([]string{}, "?dict<string, any>", "Current row as a dict, or null when exhausted."),
			"columns": fn([]string{}, "list<string>", "Column names in order."),
			"close":   fn([]string{}, "void", "Closes the result set early."),
			"all":     fn([]string{}, "list<dict<string, any>>", "Materialises every remaining row."),
			"length":  fn([]string{}, "int", "Total row count (consumes the rows)."),
			"isEmpty": fn([]string{}, "bool", "True if the result set has no rows."),
			"get":     fn([]string{"int index"}, "dict<string, any>", "Returns the row at index."),
			"first":   fn([]string{}, "?dict<string, any>", "First row or null."),
			"toList":  fn([]string{}, "list<dict<string, any>>", "Alias for all()."),
		},
	}},
	"http": {functions: map[string]functionDoc{
		"serve":              fn([]string{"string addr", "func handler"}, "void", "Starts an HTTP server."),
		"listen":             fn([]string{"string addr", "func handler"}, "Server", "Starts an HTTP server handle."),
		"close":              fn([]string{"Server server"}, "void", "Closes a server."),
		"shutdown":           fn([]string{"Server server"}, "void", "Gracefully shuts down a server."),
		"stream":             fn([]string{"int status = 200", "dict<string, string> headers = {}"}, "Response", "Creates a streaming response."),
		"streamWrite":        fn([]string{"Stream stream", "string chunk"}, "void", "Writes a streaming response chunk."),
		"streamFlush":        fn([]string{"Stream stream"}, "void", "Flushes a streaming response."),
		"streamClose":        fn([]string{"Stream stream"}, "void", "Closes a stream."),
		"get":                fn([]string{"string url"}, "Response", "Performs an HTTP GET request."),
		"post":               fn([]string{"string url", "string body"}, "Response", "Performs an HTTP POST request."),
		"postJson":           fn([]string{"string url", "any payload"}, "Response", "POSTs JSON."),
		"request":            fn([]string{"string method", "string url"}, "Response", "Performs an HTTP request."),
		"requestWithOptions": fn([]string{"dict<string, any> options"}, "Response", "Performs a configured HTTP request."),
		"jsonResponse":       fn([]string{"any payload", "int status = 200"}, "Response", "Creates a JSON response."),
		"response":           fn([]string{"string body", "int status = 200"}, "Response", "Creates a text response."),
		"newClient":          fn([]string{"dict<string, any> options = {}"}, "Client", "Builds a reusable HTTP client. Options: timeoutMs, baseUrl, headers, cookieJar (CookieJar | true), keepAlive, maxIdleConns, proxy, proxyFromEnv."),
		"newCookieJar":       fn([]string{}, "CookieJar", "Returns a new in-memory cookie jar to share across requests."),
		"build":              fn([]string{"string url"}, "Builder", "Starts a fluent request builder."),
	}, classes: map[string]string{
		"Request":  "Incoming HTTP request. Fields: method, path, query, remoteAddr, body, headers.",
		"Response": "Outgoing HTTP response. Fields: status, body, headers.",
		"Client":   "Reusable HTTP client. Build via http.newClient(opts).",
		"Builder":  "Fluent request builder. Start with http.build(url) and chain header/body/send/timeoutMs/etc.",
		"CookieJar": "Cookie jar shared across requests. Build via http.newCookieJar().",
	}, classMethods: map[string]map[string]functionDoc{
		"Request": {
			"header":    fn([]string{"string name"}, "?string", "Returns the first value of the named header, or null."),
			"json":      fn([]string{}, "any", "Parses the request body as JSON."),
			"bodyText":  fn([]string{}, "string", "Returns the body as a UTF-8 string."),
			"bodyBytes": fn([]string{}, "bytes", "Returns the body as raw bytes."),
			"toDict":    fn([]string{}, "dict<string, any>", "Snapshots the request as a plain dict."),
			"inspect":   fn([]string{}, "string", "Human-readable representation."),
		},
		"Response": {
			"withHeader": fn([]string{"string name", "string value"}, "Response", "Returns a new response with the header set."),
			"withBody":   fn([]string{"any body"}, "Response", "Returns a new response with the body replaced."),
			"withStatus": fn([]string{"int status"}, "Response", "Returns a new response with the status replaced."),
			"toDict":     fn([]string{}, "dict<string, any>", "Snapshots the response as a plain dict."),
			"inspect":    fn([]string{}, "string", "Human-readable representation."),
		},
		"Client": {
			"get":       fn([]string{"string url", "dict<string, any> options = {}"}, "Response", "Performs a GET via the client."),
			"post":      fn([]string{"string url", "any body = null", "dict<string, any> options = {}"}, "Response", "Performs a POST via the client."),
			"put":       fn([]string{"string url", "any body = null", "dict<string, any> options = {}"}, "Response", "Performs a PUT via the client."),
			"delete":    fn([]string{"string url", "dict<string, any> options = {}"}, "Response", "Performs a DELETE via the client."),
			"patch":     fn([]string{"string url", "any body = null", "dict<string, any> options = {}"}, "Response", "Performs a PATCH via the client."),
			"head":      fn([]string{"string url", "dict<string, any> options = {}"}, "Response", "Performs a HEAD via the client."),
			"options":   fn([]string{"string url", "dict<string, any> options = {}"}, "Response", "Performs an OPTIONS via the client."),
			"request":   fn([]string{"string method", "string url", "dict<string, any> options = {}"}, "Response", "Performs an arbitrary request via the client."),
			"send":      fn([]string{"dict<string, any> request"}, "Response", "Sends a fully specified request dict."),
			"close":     fn([]string{}, "void", "Releases the client and its connection pool."),
			"cookieJar": fn([]string{}, "CookieJar", "Returns the client's cookie jar."),
		},
		"Builder": {
			"method":    fn([]string{"string method"}, "Builder", "Sets the HTTP method."),
			"header":    fn([]string{"string name", "string value"}, "Builder", "Adds a header."),
			"query":     fn([]string{"string name", "string value"}, "Builder", "Appends a query-string parameter."),
			"body":      fn([]string{"any body"}, "Builder", "Sets the request body (string / bytes / dict / stream)."),
			"json":      fn([]string{"any payload"}, "Builder", "Sets a JSON body."),
			"form":      fn([]string{"dict<string, any> fields"}, "Builder", "Sets a urlencoded form body."),
			"multipart": fn([]string{"dict<string, any> fields"}, "Builder", "Sets a multipart/form-data body."),
			"timeoutMs": fn([]string{"int ms"}, "Builder", "Per-request timeout."),
			"send":      fn([]string{}, "Response", "Performs the request."),
		},
		"CookieJar": {
			"setCookie":  fn([]string{"string url", "any cookie"}, "void", "Stores a cookie scoped to the URL."),
			"cookiesFor": fn([]string{"string url"}, "list<dict<string, any>>", "Returns cookies that match the URL."),
			"clear":      fn([]string{}, "void", "Removes every cookie."),
		},
	}},
	"io": {functions: map[string]functionDoc{
		"print":          fn([]string{"any value"}, "void", "Writes a value to stdout."),
		"println":        fn([]string{"any value"}, "void", "Writes a value and newline to stdout."),
		"readText":       fn([]string{"string path"}, "string", "Reads a UTF-8 text file."),
		"writeText":      fn([]string{"string path", "string text"}, "void", "Writes a UTF-8 text file."),
		"appendText":     fn([]string{"string path", "string text"}, "void", "Appends UTF-8 text."),
		"readBytes":      fn([]string{"string path"}, "bytes", "Reads a file as bytes."),
		"writeBytes":     fn([]string{"string path", "bytes data"}, "void", "Writes bytes to a file."),
		"exists":         fn([]string{"string path"}, "bool", "Checks whether a path exists."),
		"open":           fn([]string{"string path", "string mode = \"r\""}, "File", "Opens a file resource."),
		"memory":         fn([]string{"string initial = \"\""}, "Buffer", "Creates an in-memory stream."),
		"stdin":          fn([]string{}, "Stream", "Returns stdin as a stream."),
		"stdout":         fn([]string{}, "Stream", "Returns stdout as a stream."),
		"stderr":         fn([]string{}, "Stream", "Returns stderr as a stream."),
		"read":           fn([]string{"Stream stream", "int count"}, "string", "Reads from a stream."),
		"readAll":        fn([]string{"Stream stream"}, "string", "Reads all stream content."),
		"write":          fn([]string{"Stream stream", "string text"}, "int", "Writes to a stream."),
		"writeln":        fn([]string{"Stream stream", "string text"}, "int", "Writes a line to a stream."),
		"flush":          fn([]string{"Stream stream"}, "void", "Flushes a stream."),
		"close":          fn([]string{"Stream stream"}, "void", "Closes a stream."),
		"captureStdout":  fn([]string{"func callback"}, "string", "Captures stdout produced by callback."),
		"captureStderr":  fn([]string{"func callback"}, "string", "Captures stderr produced by callback."),
		"redirectStdout": fn([]string{"Stream stream"}, "void", "Redirects stdout."),
		"redirectStderr": fn([]string{"Stream stream"}, "void", "Redirects stderr."),
		"redirectStdin":  fn([]string{"Stream stream"}, "void", "Redirects stdin."),
		"mkdir":          fn([]string{"string path", "int mode = 493"}, "void", "Creates a directory."),
		"chmod":          fn([]string{"string path", "int mode"}, "void", "Changes file permissions."),
		"tempFile":       fn([]string{"string pattern = \"\""}, "string", "Creates a temporary file."),
		"tempDir":        fn([]string{"string pattern = \"\""}, "string", "Creates a temporary directory."),
		"listDir":        fn([]string{"string path"}, "list<string>", "Lists directory entries."),
	}},
	"json": {functions: map[string]functionDoc{
		"parse":            fn([]string{"string text"}, "any", "Parses JSON text."),
		"parseAs":          fn([]string{"string text", "class target"}, "any", "Parses JSON text into an instance of the target class (uses __deserialize__ when defined, else matches dict keys to constructor parameters)."),
		"stringify":        fn([]string{"any value"}, "string", "Encodes a value as JSON."),
		"validate":         fn([]string{"string text"}, "bool", "Checks whether JSON text is valid."),
		"tryParse":         fn([]string{"string text"}, "dict<string, any>", "Parses JSON without throwing."),
		"validateDetailed": fn([]string{"string text"}, "dict<string, any>", "Returns detailed validation information."),
		"reader":           fn([]string{"Stream stream"}, "JsonStreamInterface", "Creates a JSON stream reader."),
		"stream":           fn([]string{"Stream stream"}, "JsonStreamInterface", "Creates a JSON stream reader."),
	}},
	"log": {functions: map[string]functionDoc{
		"stdout":   fn([]string{}, "Logger", "Creates a stdout logger."),
		"stderr":   fn([]string{}, "Logger", "Creates a stderr logger."),
		"file":     fn([]string{"string path"}, "Logger", "Creates a file logger."),
		"toStream": fn([]string{"IOStream stream"}, "Logger", "Creates a logger that writes JSON lines to the given IOStream. The stream's lifetime is owned by the caller - closing the logger does not close the stream."),
		"custom":   fn([]string{"LogInterface handler"}, "Logger", "Creates a custom logger."),
		"info":   fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes an info log. Pass a Logger as the first arg to target a specific sink; otherwise uses the default stderr logger."),
		"warn":   fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes a warning log. Pass a Logger as the first arg to target a specific sink; otherwise uses the default stderr logger."),
		"error":  fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes an error log. Pass a Logger as the first arg to target a specific sink; otherwise uses the default stderr logger."),
		"debug":  fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes a debug log. Pass a Logger as the first arg to target a specific sink; otherwise uses the default stderr logger."),
		"close":  fn([]string{"Logger logger"}, "void", "Closes a logger."),
	}, classes: map[string]string{
		"Logger": "A logging sink. Construct via log.stdout() / log.stderr() / log.file(path) / log.custom(handler).",
	}, classMethods: map[string]map[string]functionDoc{
		"Logger": {
			"info":  fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes an info-level event."),
			"warn":  fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes a warn-level event."),
			"error": fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes an error-level event."),
			"debug": fn([]string{"string message", "dict<string, any> context = {}"}, "void", "Writes a debug-level event."),
			"close": fn([]string{}, "void", "Flushes and closes the logger."),
		},
	}},
	"math": {functions: map[string]functionDoc{
		"abs":        fn([]string{"number value"}, "number", "Absolute value."),
		"min":        fn([]string{"number left", "number right"}, "number", "Minimum value."),
		"max":        fn([]string{"number left", "number right"}, "number", "Maximum value."),
		"clamp":      fn([]string{"number value", "number min", "number max"}, "number", "Clamps a value."),
		"sqrt":       fn([]string{"number value"}, "decimal", "Square root."),
		"pow":        fn([]string{"number base", "number exponent"}, "decimal", "Power."),
		"round":      fn([]string{"number value"}, "int", "Rounds to nearest integer."),
		"pi":         fn([]string{}, "decimal", "Returns pi."),
		"e":          fn([]string{}, "decimal", "Returns Euler's number."),
		"median":     fn([]string{"list<number> xs"}, "float", "Median (50th percentile) using linear-interpolation."),
		"percentile": fn([]string{"list<number> xs", "number p"}, "float", "p-th percentile (0..100) using R type-7 linear interpolation."),
		"quantile":   fn([]string{"list<number> xs", "number q"}, "float", "q-quantile (0..1) using R type-7 linear interpolation."),
		"mode":       fn([]string{"list<number> xs"}, "float", "Most-frequent value; ties broken by lowest value."),
	}},
	"net": {functions: map[string]functionDoc{
		"joinHostPort":  fn([]string{"string host", "int port"}, "string", "Joins host and port."),
		"splitHostPort": fn([]string{"string address"}, "dict<string, any>", "Splits host and port."),
		"lookupHost":    fn([]string{"string host"}, "list<string>", "Looks up host addresses."),
		"listenTcp":     fn([]string{"string address"}, "Listener", "Starts a TCP listener."),
		"connectTcp":    fn([]string{"string address"}, "Connection", "Connects to a TCP server."),
		"accept":        fn([]string{"Listener listener"}, "Connection", "Accepts a TCP connection."),
		"read":          fn([]string{"Connection conn", "int count"}, "bytes", "Reads from a connection."),
		"write":         fn([]string{"Connection conn", "bytes data"}, "int", "Writes to a connection."),
		"close":         fn([]string{"any resource"}, "void", "Closes a network resource."),
	}},
	"re": {functions: map[string]functionDoc{
		"test":     fn([]string{"string pattern", "string text"}, "bool", "Reports whether pattern matches text."),
		"find":     fn([]string{"string pattern", "string text"}, "string", "Returns the first match."),
		"findAll":  fn([]string{"string pattern", "string text"}, "list<string>", "Returns all matches."),
		"match":    fn([]string{"string pattern", "string text"}, "dict<string, any>", "Returns the first match with text, groups, and named fields."),
		"matchAll": fn([]string{"string pattern", "string text"}, "list<dict<string, any>>", "Returns every non-overlapping match."),
		"replace":  fn([]string{"string pattern", "string text", "string replacement"}, "string", "Replaces matches."),
		"split":    fn([]string{"string pattern", "string text"}, "list<string>", "Splits text by pattern."),
	}},
	"pcre": {functions: map[string]functionDoc{
		"test":     fn([]string{"string pattern", "string text", "string flags = \"\""}, "bool", "PCRE-compatible test. Supports lookarounds and backreferences. Flags: imsx."),
		"find":     fn([]string{"string pattern", "string text", "string flags = \"\""}, "?string", "Returns the first PCRE match or null."),
		"findAll":  fn([]string{"string pattern", "string text", "string flags = \"\""}, "list<string>", "Returns all PCRE matches."),
		"match":    fn([]string{"string pattern", "string text", "string flags = \"\""}, "?dict<string, any>", "First PCRE match with text, groups, and named fields."),
		"matchAll": fn([]string{"string pattern", "string text", "string flags = \"\""}, "list<dict<string, any>>", "Every non-overlapping PCRE match."),
		"replace":  fn([]string{"string pattern", "string replacement", "string text", "string flags = \"\""}, "string", "Replaces PCRE matches; replacement uses $1, $2, ${name} backrefs."),
		"split":    fn([]string{"string pattern", "string text", "string flags = \"\""}, "list<string>", "Splits text by PCRE pattern."),
		"quote":    fn([]string{"string text"}, "string", "Escapes regex metacharacters in a literal string."),
	}},
	"profiler": {functions: map[string]functionDoc{
		"snapshot": fn([]string{}, "dict<string, any>", "Captures wall clock, heap allocation, peak allocation, GC count, and CPU nanoseconds."),
		"delta":    fn([]string{"dict<string, any> snapshot"}, "dict<string, any>", "Returns elapsed_ms, cpu_ms, heap_alloc, allocs, and gc_count since the snapshot."),
		"memory":   fn([]string{}, "dict<string, any>", "Returns current heap_alloc, peak_alloc, heap_sys, stack_sys, total_alloc, and gc_count."),
		"cpu":      fn([]string{}, "dict<string, any>", "Returns user_ms and sys_ms CPU time for this process."),
		"peak":     fn([]string{}, "dict<string, any>", "Returns peak_alloc: highest heap bytes observed since the profiler was first used."),
	}},
	"reflect": {functions: map[string]functionDoc{
		"decorators": fn([]string{"any target"}, "list<dict<string, any>>", "Returns decorator metadata."),
		"parameters": fn([]string{"any function"}, "list<dict<string, any>>", "Returns parameter metadata."),
		"returnType": fn([]string{"any function"}, "Type", "Returns function return type."),
		"doc":        fn([]string{"any target"}, "string", "Returns the target docblock."),
		"docs":       fn([]string{"any target"}, "dict<string, any>", "Returns structured documentation metadata."),
		"typeOf":     fn([]string{"any value"}, "Type", "Returns runtime type metadata."),
		"location":   fn([]string{"any target"}, "dict<string, any>", "Returns the source position of a function or class as {module, line, column}; null when unknown (1.0.6)."),
		"exports":    fn([]string{"string module"}, "list<string>", "Lists module exports."),
		"fields":     fn([]string{"Type class"}, "list<dict<string, any>>", "Returns class fields."),
		"methods":    fn([]string{"Type class"}, "list<dict<string, any>>", "Returns class methods."),
	}},
	"sys": {functions: map[string]functionDoc{
		"exit":     fn([]string{"int code"}, "void", "Exits the process."),
		"cwd":      fn([]string{}, "string", "Returns the current working directory."),
		"getenv":   fn([]string{"string name"}, "string", "Reads an environment variable."),
		"setenv":   fn([]string{"string name", "string value"}, "void", "Sets an environment variable."),
		"args":     fn([]string{}, "list<string>", "Returns command-line arguments."),
		"run":      fn([]string{"string command", "list<string> args = []"}, "dict<string, any>", "Runs a process."),
		"sleep":    fn([]string{"int milliseconds"}, "void", "Sleeps for a duration."),
		"platform": fn([]string{}, "string", "Returns OS platform."),
		"arch":     fn([]string{}, "string", "Returns CPU architecture."),
		"tmpdir":   fn([]string{}, "string", "Returns the temporary directory."),
		"homedir":  fn([]string{}, "string", "Returns the home directory."),
	}},
	"test": {functions: map[string]functionDoc{
		"run":        fn([]string{"Type testClass", "dict<string, any> options = {}"}, "dict<string, any>", "Runs tests for a class."),
		"mock":       fn([]string{"string moduleName", "dict<string, callable> replacements"}, "void", "Patch stdlib functions with user-supplied callables for the duration of the current @test method. Auto-restored between methods."),
		"restore":    fn([]string{"string moduleName", "string fname"}, "void", "Remove a single test.mock patch."),
		"restoreAll": fn([]string{}, "void", "Clear every active test.mock patch."),
	}, classes: map[string]string{"Test": "Base class for unit tests. Subclass and annotate methods with @test. Use this.assertEquals / assertTrue / assertThrows / etc. inside test bodies."}},
	"web": {functions: map[string]functionDoc{
		"new":        fn([]string{}, "App", "Creates a web application."),
		"use":        fn([]string{"App app", "func middleware"}, "App", "Adds middleware."),
		"before":     fn([]string{"App app", "func middleware"}, "App", "Adds before middleware."),
		"after":      fn([]string{"App app", "func middleware"}, "App", "Adds after middleware."),
		"route":      fn([]string{"App app", "string method", "string path", "func handler"}, "App", "Adds a route."),
		"get":        fn([]string{"App app", "string path", "func handler"}, "App", "Adds a GET route."),
		"post":       fn([]string{"App app", "string path", "func handler"}, "App", "Adds a POST route."),
		"handle":     fn([]string{"App app", "Request request"}, "Response", "Handles a request."),
		"withHeader":     fn([]string{"Response response", "string name", "string value"}, "Response", "Returns a response with a header."),
		"parseMultipart": fn([]string{"Request request"}, "dict<string, any>", "Parses a multipart/form-data request body into {fields, files}."),
	}},
	"websocket": {functions: map[string]functionDoc{
		"connect":   fn([]string{"string url", "dict<string, any> headers = {}"}, "Connection", "Connects to a WebSocket URL."),
		"upgrade":   fn([]string{"func handler"}, "Response", "Upgrades an HTTP request to WebSocket."),
		"sendText":  fn([]string{"Connection conn", "string message"}, "void", "Sends a text frame."),
		"readText":  fn([]string{"Connection conn"}, "string", "Reads a text frame."),
		"sendBytes": fn([]string{"Connection conn", "bytes data"}, "void", "Sends a binary frame."),
		"readBytes": fn([]string{"Connection conn"}, "bytes", "Reads a binary frame."),
		"close":     fn([]string{"Connection conn"}, "void", "Closes a WebSocket connection."),
	}, classes: map[string]string{"Connection": "WebSocket connection"}, classMethods: map[string]map[string]functionDoc{
		"Connection": {
			"sendText":  fn([]string{"string message"}, "void", "Sends a text frame."),
			"readText":  fn([]string{}, "string", "Reads a text frame."),
			"sendBytes": fn([]string{"bytes data"}, "void", "Sends a binary frame."),
			"readBytes": fn([]string{}, "bytes", "Reads a binary frame."),
			"close":     fn([]string{}, "void", "Closes the connection."),
		},
	}},
	"compress": {functions: map[string]functionDoc{
		"gzip":   fn([]string{"bytes data"}, "bytes", "Compresses bytes with gzip."),
		"gunzip": fn([]string{"bytes data"}, "bytes", "Decompresses gzip-compressed bytes."),
	}},
	"archive": {functions: map[string]functionDoc{
		"zipRead":    fn([]string{"bytes data"}, "list<dict<string, any>>", "Reads a zip archive and returns a list of {name, data, isDir, size} entry dicts."),
		"zipWrite":   fn([]string{"list<dict<string, any>> entries"}, "bytes", "Writes a zip archive from a list of {name, data} entries (data may be string or bytes)."),
		"tarRead":    fn([]string{"bytes data"}, "list<dict<string, any>>", "Reads an uncompressed tar archive and returns a list of {name, data, isDir, size} entry dicts."),
		"tarWrite":   fn([]string{"list<dict<string, any>> entries"}, "bytes", "Writes a tar archive from a list of {name, data} entries."),
		"tarGzRead":  fn([]string{"bytes data"}, "list<dict<string, any>>", "Reads a gzipped tar (.tar.gz / .tgz) archive."),
		"tarGzWrite": fn([]string{"list<dict<string, any>> entries"}, "bytes", "Writes a gzipped tar (.tar.gz / .tgz) archive."),
	}},
	"crypt": {functions: map[string]functionDoc{
		"md5":                    fn([]string{"string text"}, "string", "MD5 hash as lowercase hex (legacy use only)."),
		"sha1":                   fn([]string{"string text"}, "string", "SHA-1 hash as lowercase hex (legacy use only)."),
		"sha256":                 fn([]string{"string text"}, "string", "SHA-256 hash as lowercase hex."),
		"sha512":                 fn([]string{"string text"}, "string", "SHA-512 hash as lowercase hex."),
		"sha3_256":               fn([]string{"string text"}, "string", "SHA3-256 hash as lowercase hex."),
		"blake2b":                fn([]string{"string text"}, "string", "BLAKE2b hash as lowercase hex."),
		"crc32":                  fn([]string{"string text"}, "int", "CRC32 checksum (not cryptographic)."),
		"hmacSha256":             fn([]string{"string secret", "string message"}, "string", "HMAC-SHA256 as lowercase hex."),
		"hmacSha256Bytes":        fn([]string{"string secret", "string message"}, "bytes", "HMAC-SHA256 as raw bytes. Use when the HMAC output is the next round's key (sigv4, HKDF)."),
		"randomHex":              fn([]string{"int byteCount"}, "string", "Cryptographically random hex string of 2N chars. Prefer secrets.randomHex."),
		"bcryptHash":             fn([]string{"string password", "int cost = 10"}, "string", "Hashes a password with bcrypt."),
		"bcryptVerify":           fn([]string{"string password", "string hash"}, "bool", "Verifies a bcrypt password hash."),
		"argon2idHash":           fn([]string{"string password", "dict<string, any> options = {}"}, "string", "Hashes a password with Argon2id (PHC format)."),
		"argon2idVerify":         fn([]string{"string password", "string hash"}, "bool", "Verifies an Argon2id password hash."),
		"base64Encode":           fn([]string{"string text"}, "string", "Base64-encodes a string. For bytes use encoding.base64Encode."),
		"base64Decode":           fn([]string{"string text"}, "string", "Base64-decodes a string."),
		"jwtSign":                fn([]string{"any payload", "any key", "dict<string, any> opts = {}"}, "string", "Signs a JWT. opts.alg picks the algorithm (HS256/384/512, RS256/384/512, ES256/384/512, EdDSA); defaults to HS256. key is the HMAC secret (string/bytes) or a PEM private key. \"none\" is rejected by default; opt in by passing \"none\" inside opts.allowedAlgs."),
		"jwtVerify":              fn([]string{"string token", "any key", "dict<string, any> opts = {}"}, "dict<string, any>", "Verifies a JWT. Algorithm is read from the token header; default allow-list is HS/RS/ES/EdDSA (\"none\" is rejected). Pass opts.allowedAlgs (list<string>) to override; include \"none\" to accept unsigned tokens. Returns the payload or null on failure."),
		"jwtDecode":              fn([]string{"string token"}, "dict<string, any>", "Decodes a JWT without verifying the signature; returns {header, payload}."),
		"generateRsaKey":         fn([]string{"int bits = 2048"}, "string", "Generates an RSA private key as PKCS#8 PEM."),
		"generateEcKey":          fn([]string{"string curve = \"P-256\""}, "string", "Generates an ECDSA private key as PKCS#8 PEM."),
		"generateEd25519Key":     fn([]string{}, "string", "Generates an Ed25519 private key as PKCS#8 PEM."),
		"publicKey":              fn([]string{"string privatePem"}, "string", "Extracts the public key from any supported private key PEM."),
		"generateSelfSignedCert": fn([]string{"dict<string, any> options"}, "dict<string, any>", "Generates a self-signed X.509 certificate and returns {cert, key} PEMs."),
		"generateCsr":            fn([]string{"dict<string, any> options"}, "string", "Generates a PKCS#10 CSR PEM."),
		"parseCert":              fn([]string{"string pem"}, "dict<string, any>", "Parses an X.509 certificate PEM."),
		"signCertificate":        fn([]string{"dict<string, any> options"}, "string", "Signs a CSR with a CA key + cert and returns the issued certificate PEM. Options: csr, caCert, caKey (all PEM strings), validDays (default 365), isCA (default false), dnsNames, ipAddresses, serialBits (default 128)."),
		"pkcs12Decode":           fn([]string{"bytes pfx", "string password = \"\""}, "dict<string, any>", "Decodes a PKCS#12 / PFX archive and returns {key, cert, caCerts}; key is a PKCS#8 PEM, cert is a CERTIFICATE PEM (or null), caCerts is a list of CERTIFICATE PEM strings."),
		"jweEncrypt":             fn([]string{"any payload", "any key", "dict<string, any> opts = {}"}, "string", "Encrypts a payload as a JWE compact-serialised token. opts.alg picks the key-wrap algorithm (dir, RSA-OAEP-256); opts.enc picks the content cipher (A256GCM only). dir requires a 32-byte CEK; RSA-OAEP-256 requires an RSA public key PEM."),
		"jweDecrypt":             fn([]string{"string token", "any key"}, "bytes", "Decrypts a JWE compact token using the supplied key (32-byte CEK for dir, RSA private key PEM for RSA-OAEP-256) and returns the plaintext bytes."),
		"jwtSignRS256":           fn([]string{"any payload", "string privatePem"}, "string", "Deprecated: use crypt.jwtSign(payload, privatePem, {alg: \"RS256\"})."),
		"jwtVerifyRS256":         fn([]string{"string token", "string publicPem"}, "dict<string, any>", "Deprecated: use crypt.jwtVerify(token, publicPem, {allowedAlgs: [\"RS256\"]})."),
		"jwtSignES256":           fn([]string{"any payload", "string privatePem"}, "string", "Deprecated: use crypt.jwtSign(payload, privatePem, {alg: \"ES256\"})."),
		"jwtVerifyES256":         fn([]string{"string token", "string publicPem"}, "dict<string, any>", "Deprecated: use crypt.jwtVerify(token, publicPem, {allowedAlgs: [\"ES256\"]})."),
		"aesEncrypt":             fn([]string{"bytes key", "any plaintext", "bytes associatedData = null"}, "dict<string, any>", "AES-256-GCM encrypt; returns {nonce, ciphertext}."),
		"aesDecrypt":             fn([]string{"bytes key", "bytes nonce", "bytes ciphertext", "bytes associatedData = null"}, "bytes", "AES-256-GCM decrypt; throws on authentication failure."),
		"chacha20Encrypt":        fn([]string{"bytes key", "any plaintext", "bytes associatedData = null"}, "dict<string, any>", "XChaCha20-Poly1305 encrypt; returns {nonce, ciphertext}."),
		"chacha20Decrypt":        fn([]string{"bytes key", "bytes nonce", "bytes ciphertext", "bytes associatedData = null"}, "bytes", "XChaCha20-Poly1305 decrypt; throws on authentication failure."),
	}},
	"datetime": {functions: map[string]functionDoc{
		"nowUnix":       fn([]string{}, "int", "Current Unix time in seconds."),
		"unix":          fn([]string{}, "int", "Alias for nowUnix."),
		"parse":         fn([]string{"string text", "string layout = \"\""}, "int", "Parses a date/time string into a Unix timestamp."),
		"format":        fn([]string{"int unix", "string layout"}, "string", "Formats a Unix timestamp using the layout."),
		"addSeconds":    fn([]string{"int unix", "int seconds"}, "int", "Adds seconds to a Unix timestamp."),
		"addDays":       fn([]string{"int unix", "int days"}, "int", "Adds days to a Unix timestamp."),
		"addMonths":     fn([]string{"int unix", "int months"}, "int", "Adds months to a Unix timestamp."),
		"addYears":      fn([]string{"int unix", "int years"}, "int", "Adds years to a Unix timestamp."),
		"diff":          fn([]string{"int from", "int to"}, "dict<string, any>", "Returns the difference between two Unix timestamps."),
		"toLocal":       fn([]string{"int unix", "string zone = \"UTC\""}, "string", "Renders a Unix timestamp in a zone."),
		"toUtc":         fn([]string{"int unix"}, "string", "Renders a Unix timestamp as UTC RFC3339."),
		"now":           fn([]string{"string zone = \"UTC\""}, "dict<string, any>", "Returns parts of the current date/time."),
		"nowInstant":    fn([]string{}, "Instant", "Returns the current Instant value."),
		"make":          fn([]string{"int year", "int month", "int day", "int hour = 0", "int minute = 0", "int second = 0"}, "int", "Builds a Unix timestamp from parts."),
		"formatRFC3339": fn([]string{"int unix"}, "string", "Renders RFC3339 ('2006-01-02T15:04:05Z')."),
		"formatDate":    fn([]string{"int unix"}, "string", "Renders YYYY-MM-DD."),
		"formatTime":    fn([]string{"int unix"}, "string", "Renders HH:MM:SS."),
		"formatHTTP":    fn([]string{"int unix"}, "string", "Renders an HTTP-date (RFC 1123)."),
		"partsInZone":   fn([]string{"int unix", "string zone"}, "dict<string, any>", "Returns date/time parts in the given zone."),
		"parseRFC3339":  fn([]string{"string text"}, "int", "Parses an RFC3339 timestamp into Unix seconds."),
		"weekdayName":   fn([]string{"int unix"}, "string", "Returns the weekday name."),
		"monthName":     fn([]string{"int unix"}, "string", "Returns the month name."),
	}, classes: map[string]string{
		"Instant":  "Immutable point-in-time value.",
		"Duration": "Immutable duration value.",
		"Zone":     "Time zone identifier.",
	}, classMethods: map[string]map[string]functionDoc{
		"Instant": {
			"toUnix":        fn([]string{}, "int", "Unix seconds."),
			"toUnixMillis":  fn([]string{}, "int", "Unix milliseconds."),
			"toUnixNanos":   fn([]string{}, "int", "Unix nanoseconds."),
			"format":        fn([]string{"string layout"}, "string", "Formats with a layout string."),
			"formatRFC3339": fn([]string{}, "string", "Renders RFC3339."),
			"formatHTTP":    fn([]string{}, "string", "Renders an HTTP-date (RFC 1123)."),
			"add":           fn([]string{"Duration duration"}, "Instant", "Returns a new Instant offset by the duration."),
			"sub":           fn([]string{"Duration duration"}, "Instant", "Returns a new Instant offset back by the duration."),
			"diff":          fn([]string{"Instant other"}, "Duration", "Returns the duration between two Instants."),
			"isBefore":      fn([]string{"Instant other"}, "bool", "Reports whether this Instant is earlier."),
			"isAfter":       fn([]string{"Instant other"}, "bool", "Reports whether this Instant is later."),
			"equals":        fn([]string{"Instant other"}, "bool", "Reports whether two Instants are equal."),
			"inZone":        fn([]string{"Zone zone"}, "dict<string, any>", "Returns date/time parts in the zone."),
			"toString":      fn([]string{}, "string", "Default string rendering (RFC3339)."),
		},
		"Duration": {
			"inSeconds": fn([]string{}, "int", "Whole seconds."),
			"inMillis":  fn([]string{}, "int", "Whole milliseconds."),
			"inNanos":   fn([]string{}, "int", "Whole nanoseconds."),
			"abs":       fn([]string{}, "Duration", "Absolute value."),
			"negate":    fn([]string{}, "Duration", "Returns the negated duration."),
			"add":       fn([]string{"Duration other"}, "Duration", "Sum of two durations."),
			"sub":       fn([]string{"Duration other"}, "Duration", "Difference of two durations."),
			"toString":  fn([]string{}, "string", "Human-readable rendering (e.g. \"1h2m3s\")."),
		},
		"Zone": {
			"name":     fn([]string{}, "string", "Zone name (e.g. \"UTC\", \"America/New_York\")."),
			"offset":   fn([]string{"Instant at"}, "int", "Offset from UTC in seconds at the given Instant."),
			"toString": fn([]string{}, "string", "Zone name."),
		},
	}},
	"encoding": {functions: map[string]functionDoc{
		"base64Encode": fn([]string{"any value"}, "string", "Standard Base64 encode (string or bytes input)."),
		"base64Decode": fn([]string{"string text"}, "bytes", "Standard Base64 decode."),
		"base32Encode": fn([]string{"any value"}, "string", "Base32 encode (RFC 4648, string or bytes input)."),
		"base32Decode": fn([]string{"string text"}, "bytes", "Base32 decode (padded or unpadded)."),
		"base58Encode": fn([]string{"any value"}, "string", "Base58 encode (Bitcoin/IPFS alphabet)."),
		"base58Decode": fn([]string{"string text"}, "bytes", "Base58 decode."),
		"urlEncode":    fn([]string{"string text"}, "string", "URL-encodes a string."),
		"urlDecode":    fn([]string{"string text"}, "string", "URL-decodes a string."),
		"htmlEscape":   fn([]string{"string text"}, "string", "Escapes HTML entities."),
		"htmlUnescape": fn([]string{"string text"}, "string", "Unescapes HTML entities."),
	}},
	"errors": {functions: map[string]functionDoc{
		"new":           fn([]string{"string class", "string message", "dict<string, any> fields = {}"}, "Error", "Constructs a new error value."),
		"message":       fn([]string{"Error err"}, "string", "Returns the error message."),
		"class":         fn([]string{"Error err"}, "string", "Returns the error class name."),
		"is":            fn([]string{"Error err", "string class"}, "bool", "Reports whether err's class matches (including ancestors)."),
		"wrap":          fn([]string{"Error err", "string message"}, "Error", "Wraps an error with additional context."),
		"stackTrace":    fn([]string{"Error err"}, "list<dict<string, any>>", "Returns frame entries for the error's stack trace."),
		"frames":        fn([]string{"Error err"}, "list<dict<string, any>>", "Alias for stackTrace."),
		"hasStackTrace": fn([]string{"Error err"}, "bool", "Reports whether the error carries a stack trace."),
	}},
	"freeze": {functions: map[string]functionDoc{
		"shallow":  fn([]string{"any value"}, "any", "Shallow-freezes a value (top-level fields become read-only)."),
		"deep":     fn([]string{"any value"}, "any", "Deep-freezes a value (transitively read-only)."),
		"isFrozen": fn([]string{"any value"}, "bool", "Reports whether a value is frozen."),
	}},
	"markdown": {functions: map[string]functionDoc{
		"parse":      fn([]string{"string source"}, "list<dict<string, any>>", "Parses GFM Markdown into block-node dicts."),
		"renderHtml": fn([]string{"string source"}, "string", "Renders GFM Markdown to HTML."),
		"stripText":  fn([]string{"string source"}, "string", "Extracts plain text, stripping all markup."),
	}},
	"random": {functions: map[string]functionDoc{
		"seed":     fn([]string{"int seed"}, "void", "Seeds the deterministic PRNG (not for cryptography)."),
		"next":     fn([]string{}, "int", "Returns the next pseudo-random int."),
		"intRange": fn([]string{"int min", "int max"}, "int", "Returns a pseudo-random int in [min, max]."),
		"float":    fn([]string{}, "float", "Returns a pseudo-random float in [0, 1)."),
		"bool":     fn([]string{}, "bool", "Returns a pseudo-random bool."),
		"choice":   fn([]string{"list<any> items"}, "any", "Returns a random element."),
		"shuffle":  fn([]string{"list<any> items"}, "list<any>", "Returns a shuffled copy."),
	}, classes: map[string]string{"Generator": "Stateful PRNG handle."}, classMethods: map[string]map[string]functionDoc{
		"Generator": {
			"next":     fn([]string{}, "int", "Next pseudo-random int."),
			"intRange": fn([]string{"int min", "int max"}, "int", "Pseudo-random int in [min, max]."),
			"float":    fn([]string{}, "float", "Pseudo-random float in [0, 1)."),
			"bool":     fn([]string{}, "bool", "Pseudo-random bool."),
			"choice":   fn([]string{"list<any> items"}, "any", "Random element."),
			"shuffle":  fn([]string{"list<any> items"}, "list<any>", "Shuffled copy."),
			"seed":     fn([]string{"int seed"}, "void", "Re-seeds this generator."),
		},
	}},
	"streams": {functions: map[string]functionDoc{
		"of":      fn([]string{"any source"}, "Stream", "Wraps any iterable in a Stream for fluent chaining (1.0.6)."),
		"open":    fn([]string{"string path", "string mode = \"r\""}, "IOStream", "Opens a file and wraps it in an IOStream (1.1.0)."),
		"memory":  fn([]string{"string initial = \"\""}, "IOStream", "Returns an in-memory read/write IOStream, optionally seeded (1.1.0)."),
		"stdin":   fn([]string{}, "IOStream", "Wraps stdin in an IOStream (1.1.0)."),
		"stdout":  fn([]string{}, "IOStream", "Wraps stdout in an IOStream (1.1.0)."),
		"stderr":  fn([]string{}, "IOStream", "Wraps stderr in an IOStream (1.1.0)."),
		"readAll": fn([]string{"any src", "int chunk = 4096"}, "string", "Reads everything from any value implementing __read(int) until EOF (1.1.0)."),
		"copy":    fn([]string{"any src", "any dst", "int chunk = 4096"}, "int", "Pipes from src.__read(chunk) into dst.__write(...) until EOF; returns bytes transferred (1.1.0)."),
	}, classes: map[string]string{
		"Stream":   "Lazy-by-default fluent pipeline over an iterable. Intermediate ops (map / filter / take) return a new Stream; terminal ops (toList / toSet / count / first / reduce / forEach / anyMatch / allMatch) drive iteration. Implements __iter() so a Stream is itself iterable.",
		"IOStream": "Handle-backed stream wrapper (1.1.0 F3). Methods: read(n) / readAll() / readLine() / lines() / write(buf) / writeln(buf) / flush() / close() / isClosed(). Memory-backed instances also expose toString(). Dunder protocol: __read / __write / __close / __iter (yields lines).",
	}, classMethods: map[string]map[string]functionDoc{
		"IOStream": {
			"read":     fn([]string{"int n"}, "string", "Reads up to n characters; returns less at EOF."),
			"readAll":  fn([]string{}, "string", "Reads to EOF."),
			"readLine": fn([]string{}, "?string", "Reads a single line including the newline, or null at EOF."),
			"lines":    fn([]string{}, "Stream", "Returns a Stream over the remaining lines."),
			"write":    fn([]string{"any buf"}, "int", "Writes bytes/string; returns bytes written."),
			"writeln":  fn([]string{"any buf"}, "int", "Writes buf + newline."),
			"flush":    fn([]string{}, "void", "Flushes any buffered output."),
			"close":    fn([]string{}, "void", "Closes the underlying handle."),
			"isClosed": fn([]string{}, "bool", "Reports whether the stream is closed."),
			"toString": fn([]string{}, "string", "Memory-backed streams: returns the buffer contents."),
		},
		"Stream": {
			"map":      fn([]string{"callable transform"}, "Stream", "Lazy element transformation."),
			"filter":   fn([]string{"callable predicate"}, "Stream", "Lazy element filtering."),
			"take":     fn([]string{"int n"}, "Stream", "Limits to the first n elements."),
			"skip":     fn([]string{"int n"}, "Stream", "Drops the first n elements."),
			"toList":   fn([]string{}, "list<any>", "Materialises every remaining element."),
			"toSet":    fn([]string{}, "set<any>", "Materialises every element into a set."),
			"count":    fn([]string{}, "int", "Counts the remaining elements."),
			"first":    fn([]string{}, "?any", "Returns the first element or null."),
			"reduce":   fn([]string{"any seed", "callable combiner"}, "any", "Folds the stream."),
			"forEach":  fn([]string{"callable action"}, "void", "Iterates each element."),
			"anyMatch": fn([]string{"callable predicate"}, "bool", "True if any element satisfies the predicate."),
			"allMatch": fn([]string{"callable predicate"}, "bool", "True if every element satisfies the predicate."),
		},
	}},
	"watch": {functions: map[string]functionDoc{
		"snapshot": fn([]string{"string path"}, "dict<string, any>", "Capture the current state of a file or directory for polling-based change detection."),
		"wait":     fn([]string{"string path", "dict<string, any> previous", "int timeoutMs", "int intervalMs = 250"}, "dict<string, any>", "Block until the path's state differs from the snapshot or the timeout expires."),
		"start":    fn([]string{"string path", "func callback", "dict<string, any> options = {}"}, "int", "Register an fsnotify watcher; fires callback({path, type}) for each event. Options: {recursive: bool} (1.1.0)."),
		"stop":     fn([]string{"int handle"}, "void", "Stop a watcher and wait for in-flight callbacks to finish (1.1.0)."),
	}},
	"proc": {functions: map[string]functionDoc{
		"spawn": fn([]string{"string command", "list<string> args = []", "dict<string, any> options = {}"}, "Process", "Spawn a child process and return a Process. Options: {pty: bool, cwd: string, env: dict<string, string>} (1.1.0 F4)."),
	}, classes: map[string]string{
		"Process": "Running child process (1.1.0 F4). Fields: pid, handle, stdin (IOStream), stdout (IOStream), stderr (IOStream or null in pty mode). Methods: wait() returns the exit code, kill() sends SIGKILL, signal(name) sends a named signal.",
	}, classMethods: map[string]map[string]functionDoc{
		"Process": {
			"wait":   fn([]string{}, "int", "Waits for the process to exit and returns the exit code."),
			"kill":   fn([]string{}, "void", "Sends SIGKILL."),
			"signal": fn([]string{"string name"}, "void", "Sends a named signal (e.g. \"SIGTERM\", \"SIGHUP\")."),
		},
	}},
	"sockets": {functions: map[string]functionDoc{
		"dial":  fn([]string{"string host", "int port", "dict<string, any> opts = {}"}, "Socket", "Open a TCP (or TLS via opts.tls) connection. Returns a Socket implementing the F3 stream protocol (1.2.0)."),
		"serve": fn([]string{"string host", "int port", "func handler"}, "Listener", "Bind a listener and dispatch each accepted connection to handler(socket). Close the Listener to join the accept goroutine (1.2.0)."),
	}, classes: map[string]string{
		"Socket":   "TCP / TLS connection wrapped in the F3 stream protocol (1.2.0). Methods: read(n) / readAll() / readLine() / lines() / write(buf) / writeln(buf) / close() / isClosed() / localAddr() / remoteAddr(). Dunders: __read / __write / __close / __iter.",
		"Listener": "TCP listener owning an accept-loop goroutine (1.2.0). close() stops accepting and joins; localAddr() returns the bound address (useful when port is 0).",
	}, classMethods: map[string]map[string]functionDoc{
		"Socket": {
			"read":       fn([]string{"int n"}, "string", "Reads up to n bytes; less at peer close."),
			"readAll":    fn([]string{}, "string", "Reads until the peer closes."),
			"readLine":   fn([]string{}, "?string", "Reads a single line, or null at EOF."),
			"lines":      fn([]string{}, "Stream", "Stream over remaining lines."),
			"write":      fn([]string{"any buf"}, "int", "Writes bytes/string."),
			"writeln":    fn([]string{"any buf"}, "int", "Writes buf + newline."),
			"close":      fn([]string{}, "void", "Closes the connection."),
			"isClosed":   fn([]string{}, "bool", "Reports whether the socket is closed."),
			"localAddr":  fn([]string{}, "string", "Local \"host:port\"."),
			"remoteAddr": fn([]string{}, "string", "Remote \"host:port\"."),
		},
		"Listener": {
			"close":     fn([]string{}, "void", "Stops accepting and joins the accept goroutine."),
			"localAddr": fn([]string{}, "string", "Bound \"host:port\" (useful when port was 0)."),
		},
	}},
	"ssh": {functions: map[string]functionDoc{
		"connect": fn([]string{"string target", "dict<string, any> opts = {}"}, "SSHClient", "Connect to an SSH server. Target is \"user@host\" or \"host\". Auth opts: password / privateKey / privateKeyFile / passphrase / agent. Host key opts: knownHostsFile / insecureSkipHostKey. Other: port, timeoutMs (1.2.0)."),
	}, classes: map[string]string{
		"SSHClient":  "Connected SSH session (1.2.0). Methods: exec(cmd) returns ExecResult; spawn(cmd) returns SSHSession with IOStream pipes; upload / download / sftpList / sftpRemove / sftpMkdir / sftpOpen for SFTP; forwardLocal / forwardRemote return SSHTunnel; close() shuts the connection.",
		"SSHSession": "Long-running remote command (1.2.0). Fields: handle, stdin / stdout / stderr (IOStream). Methods: wait() returns exit code, kill() sends SIGKILL, signal(name) sends a named signal.",
		"SSHTunnel":  "Local or remote port forward (1.2.0). addr() returns the bound address; close() stops the accept loop and joins.",
		"ExecResult": "Result of SSHClient.exec(cmd) (1.2.0). Fields: stdout (string), stderr (string), exitCode (int).",
	}, classMethods: map[string]map[string]functionDoc{
		"SSHClient": {
			"exec":          fn([]string{"string cmd"}, "ExecResult", "Runs cmd remotely; returns stdout/stderr/exitCode."),
			"spawn":         fn([]string{"string cmd"}, "SSHSession", "Starts cmd with streaming stdin/stdout/stderr pipes."),
			"upload":        fn([]string{"string localPath", "string remotePath"}, "void", "SFTP upload."),
			"download":      fn([]string{"string remotePath", "string localPath"}, "void", "SFTP download."),
			"sftpList":      fn([]string{"string remotePath"}, "list<dict<string, any>>", "Lists a remote directory."),
			"sftpRemove":    fn([]string{"string remotePath"}, "void", "Deletes a remote file."),
			"sftpMkdir":     fn([]string{"string remotePath"}, "void", "Creates a remote directory."),
			"sftpOpen":      fn([]string{"string remotePath", "string mode = \"r\""}, "IOStream", "Opens a remote file as an IOStream."),
			"forwardLocal":  fn([]string{"int localPort", "string remoteHost", "int remotePort"}, "SSHTunnel", "Local -> remote port forward."),
			"forwardRemote": fn([]string{"int remotePort", "string localHost", "int localPort"}, "SSHTunnel", "Remote -> local port forward."),
			"close":         fn([]string{}, "void", "Closes the SSH connection."),
		},
		"SSHSession": {
			"wait":   fn([]string{}, "int", "Waits for the remote command to finish; returns exit code."),
			"kill":   fn([]string{}, "void", "Sends SIGKILL to the remote process."),
			"signal": fn([]string{"string name"}, "void", "Sends a named signal to the remote process."),
		},
		"SSHTunnel": {
			"addr":  fn([]string{}, "string", "Bound \"host:port\"."),
			"close": fn([]string{}, "void", "Stops the tunnel and joins the accept loop."),
		},
	}},
	"strings": {classes: map[string]string{
		"StringBuilder": "Builder-backed string accumulator. Amortised O(n) append for tight-loop assembly; call dispose() to release the handle in long-running processes.",
	}},
	"strbuilder": {functions: map[string]functionDoc{
		"new":        fn([]string{"string initial = \"\""}, "StringBuilder", "Creates a new builder handle, optionally pre-seeded."),
		"append":     fn([]string{"StringBuilder sb", "string s"}, "StringBuilder", "Appends a fragment; returns the same handle."),
		"appendLine": fn([]string{"StringBuilder sb", "string s"}, "StringBuilder", "Appends a fragment followed by a newline."),
		"build":      fn([]string{"StringBuilder sb"}, "string", "Materialises the accumulated content."),
		"length":     fn([]string{"StringBuilder sb"}, "int", "Current byte length."),
		"clear":      fn([]string{"StringBuilder sb"}, "StringBuilder", "Resets the buffer to empty."),
		"dispose":    fn([]string{"StringBuilder sb"}, "void", "Releases the handle. Safe to call multiple times."),
	}, classes: map[string]string{
		"StringBuilder": "Builder-backed string accumulator. Amortised O(n) append for tight-loop assembly; call dispose() to release the handle in long-running processes.",
	}, classMethods: map[string]map[string]functionDoc{
		"StringBuilder": {
			"append":     fn([]string{"string s"}, "StringBuilder", "Appends a fragment; returns the same handle."),
			"appendLine": fn([]string{"string s"}, "StringBuilder", "Appends a fragment followed by a newline."),
			"build":      fn([]string{}, "string", "Materialises the accumulated content."),
			"length":     fn([]string{}, "int", "Current byte length."),
			"clear":      fn([]string{}, "StringBuilder", "Resets the buffer to empty."),
			"dispose":    fn([]string{}, "void", "Releases the handle."),
		},
	}},
	"path": {functions: map[string]functionDoc{
		"join":  fn([]string{"string ...parts"}, "string", "Joins path segments using the platform separator."),
		"clean": fn([]string{"string p"}, "string", "Lexically normalises a path."),
		"base":  fn([]string{"string p"}, "string", "Returns the last segment."),
		"dir":   fn([]string{"string p"}, "string", "Returns everything before the last segment."),
		"ext":   fn([]string{"string p"}, "string", "Returns the file extension including the leading dot."),
		"abs":   fn([]string{"string p"}, "string", "Returns the absolute path."),
		"rel":   fn([]string{"string base", "string target"}, "string", "Returns a relative path from base to target."),
		"glob":  fn([]string{"string pattern"}, "list<string>", "Expands a glob pattern. Supports `**` for recursive matches (Python-style)."),
	}},
	"csv": {functions: map[string]functionDoc{
		"parse":     fn([]string{"string text", "dict<string, any> options = {}"}, "list<list<string>>", "Parses CSV text into a list of rows; each row is a list of cell strings. Options: delimiter (single char), trimSpace (bool)."),
		"parseDict": fn([]string{"string text", "dict<string, any> options = {}"}, "list<dict<string, string>>", "Parses CSV text with the first row as headers; returns a list of dicts keyed by header name."),
		"stringify": fn([]string{"list<list<any>> rows", "dict<string, any> options = {}"}, "string", "Serialises a list of rows as CSV text. Options: delimiter (single char)."),
		"reader":    fn([]string{"any source"}, "CsvReader", "Returns a streaming reader over a file path, bytes, or string source. Use .hasNext / .next / .close."),
		"stream":    fn([]string{"any source", "any handler"}, "void", "Drives a streaming CSV parse through callback methods on handler."),
	}},
	"secrets": {functions: map[string]functionDoc{
		"randomBytes":       fn([]string{"int n"}, "bytes", "Cryptographically random N bytes."),
		"randomInt":         fn([]string{"int min", "int max"}, "int", "Cryptographically random int in [min, max]."),
		"randomHex":         fn([]string{"int n"}, "string", "Cryptographically random hex string (2N chars)."),
		"randomBase64":      fn([]string{"int n"}, "string", "Cryptographically random URL-safe Base64."),
		"constantTimeEqual": fn([]string{"any a", "any b"}, "bool", "Constant-time equality comparison; both args same type and length."),
	}},
	"template": {functions: map[string]functionDoc{
		"renderString": fn([]string{"string template", "dict<string, any> context"}, "string", "Renders an inline template string."),
		"load":         fn([]string{"string path"}, "Template", "Loads a template from disk."),
	}, classes: map[string]string{
		"Template": "Compiled template.",
		"Engine":   "Template engine with reusable configuration.",
	}, classMethods: map[string]map[string]functionDoc{
		"Template": {
			"render":      fn([]string{"dict<string, any> context"}, "string", "Renders the template against a context dict."),
			"renderToFile": fn([]string{"string path", "dict<string, any> context"}, "void", "Renders and writes to a file."),
			"name":        fn([]string{}, "string", "Returns the template name."),
		},
		"Engine": {
			"load":      fn([]string{"string path"}, "Template", "Loads and compiles a template from disk."),
			"parse":     fn([]string{"string source"}, "Template", "Compiles an inline template string."),
			"register":  fn([]string{"string name", "callable helper"}, "void", "Registers a named helper available to all templates."),
			"directory": fn([]string{"string path"}, "Engine", "Sets the template root directory; returns the engine."),
		},
	}},
	"time": {functions: map[string]functionDoc{
		"now":     fn([]string{}, "int", "High-resolution timestamp in milliseconds (monotonic)."),
		"elapsed": fn([]string{"int start"}, "int", "Milliseconds elapsed since start (from time.now)."),
		"sleep":   fn([]string{"int milliseconds"}, "void", "Blocks the calling goroutine."),
	}},
	"toml": {functions: map[string]functionDoc{
		"parse":            fn([]string{"string text"}, "any", "Parses TOML into a dict/list value; throws on error."),
		"parseAs":          fn([]string{"string text", "class target"}, "any", "Parses TOML into an instance of the target class."),
		"tryParse":         fn([]string{"string text"}, "any", "Parses TOML or returns null on error."),
		"stringify":        fn([]string{"any value"}, "string", "Serialises a value as TOML."),
		"validate":         fn([]string{"string text"}, "bool", "Reports whether text is valid TOML."),
		"validateDetailed": fn([]string{"string text"}, "dict<string, any>", "Detailed validation result."),
	}},
	"url": {functions: map[string]functionDoc{
		"parse":     fn([]string{"string text"}, "URL", "Parses a URL string."),
		"stringify": fn([]string{"URL url"}, "string", "Serialises a URL value."),
		"encode":    fn([]string{"string text"}, "string", "URL-encodes a string."),
		"decode":    fn([]string{"string text"}, "string", "URL-decodes a string."),
		"joinPath":  fn([]string{"string base", "string ...parts"}, "string", "Joins URL path segments."),
	}, classes: map[string]string{"URL": "Parsed URL value with scheme/host/path/query parts."}, classMethods: map[string]map[string]functionDoc{
		"URL": {
			"scheme":       fn([]string{}, "string", "Returns the URL scheme."),
			"host":         fn([]string{}, "string", "Returns the host without port."),
			"port":         fn([]string{}, "string", "Returns the port (empty when default)."),
			"path":         fn([]string{}, "string", "Returns the path component."),
			"query":        fn([]string{}, "dict<string, string>", "Returns the query parameters as a dict."),
			"fragment":     fn([]string{}, "string", "Returns the fragment after #."),
			"toString":     fn([]string{}, "string", "Returns the full URL as a string."),
			"toDict":       fn([]string{}, "dict<string, any>", "Returns a dict with scheme/host/port/path/query/fragment keys."),
			"withScheme":   fn([]string{"string scheme"}, "URL", "Returns a new URL with the scheme replaced."),
			"withHost":     fn([]string{"string host"}, "URL", "Returns a new URL with the host replaced."),
			"withPath":     fn([]string{"string path"}, "URL", "Returns a new URL with the path replaced."),
			"withQuery":    fn([]string{"any query"}, "URL", "Returns a new URL with the query replaced (dict or raw string)."),
			"withFragment": fn([]string{"string fragment"}, "URL", "Returns a new URL with the fragment replaced."),
			"resolve":      fn([]string{"any ref"}, "URL", "Resolves a reference URL against this base."),
			"normalize":    fn([]string{}, "URL", "Cleans the path and re-encodes the query string."),
		},
	}},
	"uuid": {functions: map[string]functionDoc{
		"v1":            fn([]string{}, "bytes", "Time-based UUIDv1."),
		"v4":            fn([]string{}, "bytes", "Random UUIDv4."),
		"v7":            fn([]string{}, "bytes", "Time-ordered UUIDv7."),
		"v3":            fn([]string{"bytes namespace", "string name"}, "bytes", "MD5-namespaced UUIDv3."),
		"v5":            fn([]string{"bytes namespace", "string name"}, "bytes", "SHA1-namespaced UUIDv5."),
		"parse":         fn([]string{"string text"}, "bytes", "Parses a UUID string into bytes."),
		"isValid":       fn([]string{"string text"}, "bool", "Reports whether text is a valid UUID string."),
		"nil":           fn([]string{}, "bytes", "The all-zero UUID."),
		"toBytes":       fn([]string{"string text"}, "bytes", "Alias for parse."),
		"fromBytes":     fn([]string{"bytes data"}, "string", "Renders a UUID-byte value as a canonical UUID string."),
		"namespaceDNS":  fn([]string{}, "bytes", "The DNS UUID namespace."),
		"namespaceURL":  fn([]string{}, "bytes", "The URL UUID namespace."),
		"namespaceOID":  fn([]string{}, "bytes", "The OID UUID namespace."),
		"namespaceX500": fn([]string{}, "bytes", "The X.500 UUID namespace."),
		"ulid":          fn([]string{}, "string", "Returns a ULID (sortable random 128-bit identifier)."),
	}},
	"xml": {functions: map[string]functionDoc{
		"parse":            fn([]string{"string text"}, "any", "Parses XML into a value; throws on error."),
		"parseAs":          fn([]string{"string text", "class target"}, "any", "Parses XML into an instance of the target class."),
		"tryParse":         fn([]string{"string text"}, "any", "Parses XML or returns null on error."),
		"stringify":        fn([]string{"any value"}, "string", "Serialises a value as XML."),
		"validate":         fn([]string{"string text"}, "bool", "Reports whether text is valid XML."),
		"validateDetailed": fn([]string{"string text"}, "dict<string, any>", "Detailed validation result."),
	}},
	"yaml": {functions: map[string]functionDoc{
		"parse":            fn([]string{"string text"}, "any", "Parses YAML into a value; throws on error."),
		"parseAs":          fn([]string{"string text", "class target"}, "any", "Parses YAML into an instance of the target class."),
		"tryParse":         fn([]string{"string text"}, "any", "Parses YAML or returns null on error."),
		"stringify":        fn([]string{"any value"}, "string", "Serialises a value as YAML."),
		"validate":         fn([]string{"string text"}, "bool", "Reports whether text is valid YAML."),
		"validateDetailed": fn([]string{"string text"}, "dict<string, any>", "Detailed validation result."),
	}},
}

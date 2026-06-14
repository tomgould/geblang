package bytecode_test

// Parity tests run each snippet through both the evaluator and the VM and
// assert that the printed output matches.  They pin the intersection of
// features that both execution paths are expected to support identically.

import (
	"testing"
)

// stdlibModuleLoader is a minimal bytecode-side module loader used by
// parity tests for source-distributed stdlib modules (time.scheduler,
// async.rate, etc.). It resolves canonical module paths via the standard
// resolver (which walks ancestors to find stdlib/), compiles each
// imported source module to bytecode on demand, and caches the result
// inside the loader struct.

func TestParityStringOperations(t *testing.T) {
	runParity(t, `import io;
string s = "hello";
io.println(s + " world");
io.println(s.length());
io.println(s.upper());
io.println(s.contains("ell"));
io.println(s.replace("l", "r"));
io.println(s[1..<3]);
`, "hello world\n5\nHELLO\ntrue\nherro\nel\n")
}

func TestParityPrimitiveStringMethods(t *testing.T) {
	runParity(t, `import io;
string s = "  hello world  ";
io.println(s.trim());
io.println(s.trim().split(" ").length());
io.println("abc".startsWith("ab"));
io.println("abc".endsWith("bc"));
io.println("abc".indexOf("b"));
`, "hello world\n2\ntrue\ntrue\n1\n")
}

func TestParityStringMethods(t *testing.T) {
	runParity(t, `import io;
let s = "  hello  ";
io.println(s.trimStart());
io.println(s.trimEnd());
io.println("ha".repeat(3));
io.println("x".padStart(5));
io.println("x".padEnd(5, "-"));
let chars = "abc".chars();
io.println(chars.length() as string);
io.println(chars.get(0));
io.println("A".codePointAt(0) as string);
`, "hello  \n  hello\nhahaha\n    x\nx----\n3\na\n65\n")
}

func TestParityStringFormat(t *testing.T) {
	runParity(t, `import io;
io.println("Hello, %s!".format("World"));
io.println("x=%d, y=%d".format(3, 7));
io.println("pi=%.2f".format(3.14159));
io.println("%x".format(255));
io.println("%05d".format(42));
`, "Hello, World!\nx=3, y=7\npi=3.14\nff\n00042\n")
}

func TestParityInterpolateVar(t *testing.T) {
	runParity(t, `import io;
let name = "world";
io.println("Hello ${name}!");
`, "Hello world!\n")
}

func TestParityInterpolateExpr(t *testing.T) {
	runParity(t, `import io;
io.println("${1 + 2}");
`, "3\n")
}

func TestParityInterpolateMultiple(t *testing.T) {
	runParity(t, `import io;
let a = 3;
let b = 4;
io.println("${a} + ${b} = ${a + b}");
`, "3 + 4 = 7\n")
}

func TestParityInterpolateNested(t *testing.T) {
	runParity(t, `import io;
func greet(string who): string {
    return "Hello, ${who}!";
}
io.println(greet("Geblang"));
`, "Hello, Geblang!\n")
}

func TestParityInterpolateLiteralOnly(t *testing.T) {
	runParity(t, `import io;
io.println("no interpolation");
`, "no interpolation\n")
}

func TestParityStringInterpolationDoubleQuoted(t *testing.T) {
	runParity(t, `import io;
io.println("Hello ${"world"}");
io.println("Sum: ${1 + 2}");
io.println("Brace: ${"has}brace"}");
io.println("Open: ${"has{brace"}");
io.println("Dict: ${{"key": "val"}["key"]}");
let d = {"x": 42};
io.println("Lookup: ${d["x"]}");
`, "Hello world\nSum: 3\nBrace: has}brace\nOpen: has{brace\nDict: val\nLookup: 42\n")
}

func TestParityFStringFormatSpecs(t *testing.T) {
	runParity(t, `import io;
let pi = 3.14159;
io.println("${pi:.2f}");
io.println("${pi:.4f}");
`, "3.14\n3.1416\n")

	runParity(t, `import io;
io.println("${100000:,}");
io.println("${1234567:,}");
`, "100,000\n1,234,567\n")

	runParity(t, `import io;
io.println("${42:>5}|");
io.println("${42:<5}|");
io.println("${42:^5}|");
io.println("${42:05}");
`, "   42|\n42   |\n 42  |\n00042\n")

	runParity(t, `import io;
io.println("${255:x}");
io.println("${255:X}");
io.println("${255:o}");
io.println("${15:b}");
`, "ff\nFF\n377\n1111\n")

	runParity(t, `import io;
io.println("${0.5:%}");
io.println("${42:+d}");
io.println("${-42:+d}");
`, "50.000000%\n+42\n-42\n")

	runParity(t, `import io;
let name = "Ada";
io.println("${name:>10}|");
io.println("${name:<10}|");
io.println("${name:^10}|");
io.println("${name:.2}");
`, "       Ada|\nAda       |\n   Ada    |\nAd\n")

	// Spec separator inside a ternary should not be confused for format-spec.
	runParity(t, `import io;
let x = 5;
io.println("${true ? x : 0}");
io.println("${(true ? x : 0):03d}");
`, "5\n005\n")
}

// TestParityStringModule guards the new `string` module
// introduced in 1.0.2 - a namespace for static / factory
// functions that don't fit as instance methods on a string
// value. Pairs with the existing `.codePointAt(i)` instance
// method (round-trips through fromCodePoint).
func TestParityStringModule(t *testing.T) {
	runParity(t, `import io;
import string;

io.println(string.fromCodePoint(65));
io.println(string.fromCodePoint(8364));
io.println(string.fromCodePoint("€".codePointAt(0)));
io.println(string.fromCodePoints([72, 105, 33]));
io.println(string.compare("apple", "banana"));
io.println(string.compare("banana", "apple"));
io.println(string.compare("same", "same"));
io.println(string.equalsFold("Hello", "HELLO"));
io.println(string.equalsFold("abc", "abd"));
`, "A\n€\n€\nHi!\n-1\n1\n0\ntrue\nfalse\n")
}

// TestParityStringAddFastPath guards an `OpAdd` reorder in the VM:
// `string + string` is fast-pathed before the binary-operator-method
// detour (`callBinaryOperatorMethod` does nothing useful when the
// left operand is a `runtime.String` since the built-in string type
// has no `__add` magic method). Verifies the common path AND that an
// instance with `__add` on the LEFT still routes through method
// dispatch (the fast path only matches when both operands are
// strings).
func TestParityStringAddFastPath(t *testing.T) {
	runParity(t, `import io;

io.println("hello " + "world");
io.println("" + "x");
io.println("a" + "" + "b" + "c");

class Adder {
    int n;
    func Adder(int n) { this.n = n; }
    func __add(int other): int { return this.n + other; }
}

let a = Adder(10);
io.println(a + 5);
`, "hello world\nx\nabc\n15\n")
}

// TestParityAddStringConst exercises the OpAddStringConst opcode
// emitted when one operand of `+` is a static string literal.
func TestParityAddStringConst(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 5; i++) {
    if (i % 3 == 0) {
        acc = acc + "a";
    } else if (i % 2 == 0) {
        acc = acc + "bc";
    } else {
        acc = acc + "1";
    }
}
io.println(acc);
`, "a1bcabc\n")
}

// TestParityCastDunders exercises __string/__int/__float/__bool/__decimal/__bytes
// cast-overload dunders. Both backends call the dunder when the
// receiver is an instance and the target is a built-in primitive.
func TestParityStringAccumulatorLoop(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 200; i++) {
    acc = acc + "x";
}
io.println(acc.length());
io.println(acc.substring(0, 3));
io.println(acc.substring(197, 200));
`, "200\nxxx\nxxx\n")
}

func TestParityStringAccumulatorInterleavedRead(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 10; i++) {
    acc = acc + "ab";
    if (i == 4) {
        io.println(acc);
    }
}
io.println(acc);
`, "ababababab\nabababababababababab\n")
}

func TestParityStringRegexMethods(t *testing.T) {
	runParity(t, `import io;

io.println("foo,bar,baz".splitRegex(",").length);
io.println("foo, bar; baz".replaceRegex("[,;] *", "|"));
io.println("foo123".matchesRegex("[a-z]+[0-9]+"));
io.println("only-letters".matchesRegex("[0-9]+"));
`, "3\nfoo|bar|baz\ntrue\nfalse\n")
}

func TestParityCSVParseAndStringify(t *testing.T) {
	runParity(t, `import io;
import csv;

let text = "a,b,c\n1,2,3\n4,5,6";
let rows = csv.parse(text);
io.println(rows.length);
io.println(rows[1][1]);

let dicts = csv.parseDict(text);
io.println(dicts[0]["b"]);
io.println(dicts[1]["c"]);
`, "3\n2\n2\n6\n")
}

func TestParityStringAccumulatorEscapesAssignment(t *testing.T) {
	runParity(t, `import io;

string acc = "";
for (int i = 0; i < 5; i++) {
    acc = acc + "ab";
}
string copy = acc;
acc = acc + "cd";
io.println(copy);
io.println(acc);
`, "ababababab\nabababababcd\n")
}

// TestParityStringRelational verifies lexicographic string
// comparison via <, <=, >, >= works on both backends. Previously
// the VM dispatched relational ops only through NumericCompare,
// rejecting strings with "comparison expects compatible numeric
// operands"; the evaluator's compareValues handled them via
// runtime.String. Now both share native.NumericCompare which
// covers strings too.
func TestParityStringRelational(t *testing.T) {
	runParity(t, `import io;
io.println(("apple" < "banana") as string);
io.println(("apple" <= "apple") as string);
io.println(("zebra" > "apple") as string);
io.println(("zebra" >= "zebra") as string);
io.println(("apple" > "zebra") as string);
let ch = "5";
io.println((ch >= "0" && ch <= "9") as string);
let letter = "m";
io.println((letter >= "a" && letter <= "z") as string);
`, "true\ntrue\ntrue\ntrue\nfalse\ntrue\ntrue\n")
}

// New string ergonomics methods behave identically on both backends.
func TestParityStringErgonomics(t *testing.T) {
	runParity(t, `import io;
io.println("hELLO".capitalize());
io.println("hELLO wORLD".title());
io.println("  ".isBlank());
io.println("x".isBlank());
io.println("a\nb\r\nc\n".lines());
io.println("".lines());
io.println("\n".lines());
io.println("foobar".removePrefix("foo"));
io.println("foobar".removePrefix("xyz"));
io.println("foobar".removeSuffix("bar"));
io.println("HELLO".equalsIgnoreCase("hello"));
io.println("Hello World".containsIgnoreCase("WORLD"));
io.println("Hello World".containsIgnoreCase("xyz"));
`, "Hello\nHello World\ntrue\nfalse\n[\"a\", \"b\", \"c\"]\n[]\n[\"\"]\nbar\nfoobar\nfoo\ntrue\ntrue\nfalse\n")
}

// Numeric-check predicates (string isInt/isDecimal/isNumeric, float.isInt,
// decimal.isInt) agree on both backends and reuse the toInt/toDecimal parse.
// A bare 3.0 literal is a decimal; 3.0f is a float.
func TestParityNumericCheckMethods(t *testing.T) {
	runParity(t, `import io;
import math;
io.println("42".isInt());
io.println("-7".isInt());
io.println("0xFF".isInt());
io.println("1_000".isInt());
io.println("3.5".isInt());
io.println("abc".isInt());
io.println("".isInt());
io.println("3.5".isDecimal());
io.println("42".isDecimal());
io.println("abc".isDecimal());
io.println("3.5".isNumeric());
io.println("42".isNumeric());
io.println("abc".isNumeric());
io.println((3.0f).isInt());
io.println((3.5f).isInt());
io.println(math.nan().isInt());
io.println(math.inf().isInt());
io.println((7.0).isInt());
io.println((7.5).isInt());
`, "true\ntrue\ntrue\ntrue\nfalse\nfalse\nfalse\ntrue\ntrue\nfalse\ntrue\ntrue\nfalse\ntrue\nfalse\nfalse\nfalse\ntrue\nfalse\n")
}

// Escape sequences (\n, \t, \u{...}) are decoded inside interpolated
// strings, identically on both backends.
func TestParityInterpolatedStringEscapes(t *testing.T) {
	runParity(t, `import io;
let name = "world";
io.println("hi\t${name}\nbye \u{1F600}");
`, "hi\tworld\nbye \U0001F600\n")
}

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

// TestParityNonExhaustiveMatchRuntime pins that a non-exhaustive enum match
// (which the analyzer warns on at check time) is unchanged at runtime on both
// backends: covered variants return, an uncovered variant throws identically.
func TestParityNonExhaustiveMatchRuntime(t *testing.T) {
	runParity(t, `import io;
enum Color { Red, Green, Blue }
func describe(Color c): string {
    return match (c) {
        case Color.Red => "red";
        case Color.Green => "green";
    };
}
io.println(describe(Color.Red));
io.println(describe(Color.Green));
try {
    io.println(describe(Color.Blue));
} catch (Error e) {
    io.println("unmatched");
}
`, "red\ngreen\nunmatched\n")
}

func TestParityMatchExpression(t *testing.T) {
	runParity(t, `import io;
func classify(int n): string {
    return match(n) {
        case 1 => "one";
        case 2 => "two";
        default => "other";
    };
}
io.println(classify(1));
io.println(classify(2));
io.println(classify(99));
`, "one\ntwo\nother\n")
}

func TestParityDestructuringForIn(t *testing.T) {
	runParity(t, `import io;
let pairs = [["a", 1], ["b", 2], ["c", 3]];
for (name, value in pairs) {
    io.println(name + ":" + (value as string));
}
`, "a:1\nb:2\nc:3\n")
}

func TestParityDestructuring(t *testing.T) {
	runParity(t, `import io;
let [a, b, c] = [10, 20, 30];
io.println(a as string);
io.println(b as string);
io.println(c as string);
let {name, age} = {"name": "Alice", "age": 42};
io.println(name);
io.println(age as string);
func coords(): list { return [7, 8]; }
let [x, y] = coords();
io.println(x as string);
io.println(y as string);
let first = 0;
let second = 0;
[first, second] = [100, 200];
io.println(first as string);
io.println(second as string);
name = "";
age = 0;
{name, age} = {"name": "Bob", "age": 51};
io.println(name);
io.println(age as string);
`, "10\n20\n30\nAlice\n42\n7\n8\n100\n200\nBob\n51\n")
}

// Arrow-bodied match STATEMENT arms must execute their expression (the
// documented `case "serve" => startServer();` action form). Both
// backends previously matched the case and silently dropped the body.
func TestParityMatchStatementArrowArmsExecute(t *testing.T) {
	runParity(t, `import io;
match (5) {
    case 5 => io.println("five");
    default => io.println("def");
}
match ([9, 2]) {
    case [a, b] if (a > b) => io.println("big ${a} ${b}");
    default => io.println("def2");
}
match ("x") {
    case "y" => io.println("wrong");
    default => io.println("fallback");
}
let hits = [];
match (1) {
    case 1 => hits.push("one");
}
io.println("${hits}");
`, "five\nbig 9 2\nfallback\n[\"one\"]\n")
}

// List patterns accept literal elements alongside binders (1.16.0):
// literals match by equality, binders capture, types still guard.
func TestParityListPatternLiteralElements(t *testing.T) {
	runParity(t, `import io;
func describe(list<any> v): string {
    return match (v) {
        case [1, x, 3] => "mid ${x}";
        case [0, 0] => "zeros";
        case ["go", n] if (n > 10) => "big go ${n}";
        case ["go", n] => "go ${n}";
        case [-1, y] => "neg ${y}";
        case [true, s] => "flag ${s}";
        case [int a, 9] => "ends nine ${a}";
        default => "other";
    };
}
io.println(describe([1, 2, 3]));
io.println(describe([0, 0]));
io.println(describe(["go", 50]));
io.println(describe(["go", 5]));
io.println(describe([-1, 7]));
io.println(describe([true, "on"]));
io.println(describe([4, 9]));
io.println(describe([9, 9, 9]));
match ([1, 99]) {
    case [1, v] => io.println("stmt ${v}");
    default => io.println("stmt-def");
}
`, "mid 2\nzeros\nbig go 50\ngo 5\nneg 7\nflag on\nends nine 4\nother\nstmt 99\n")
}

func TestParityEnumSimpleVariants(t *testing.T) {
	runParity(t, `import io;

enum Color { Red, Green, Blue }

Color c = Color.Red;
io.println(c);
io.println(Color.Green);
io.println(c == Color.Red);
io.println(c == Color.Blue);
`, "Color.Red\nColor.Green\ntrue\nfalse\n")
}

func TestParityEnumTaggedVariants(t *testing.T) {
	runParity(t, `import io;

enum Result { Ok(string), Err(string) }

Result r = Result.Ok("hello");
io.println(r);
Result e = Result.Err("oops");
io.println(e);
io.println(r == Result.Ok("hello"));
io.println(r == Result.Err("hello"));
`, "Result.Ok(hello)\nResult.Err(oops)\ntrue\nfalse\n")
}

func TestParityEnumInstanceof(t *testing.T) {
	runParity(t, `import io;

enum Color { Red, Green, Blue }
enum Result { Ok(string), Err(string) }

Color c = Color.Green;
Result r = Result.Ok("hi");
Result e = Result.Err("bad");

io.println(c instanceof Color);
io.println(r instanceof Result);
io.println(r instanceof Result.Ok);
io.println(e instanceof Result.Ok);
io.println(e instanceof Result.Err);
`, "true\ntrue\ntrue\nfalse\ntrue\n")
}

func TestParityEnumValuesAndFromName(t *testing.T) {
	runParity(t, `import io;

enum Color { Red, Green, Blue }

list<Color> all = Color.values();
io.println(all.length());
for (Color c in all) {
    io.println(c);
}
io.println(Color.fromName("Green"));
io.println(Color.fromName("Green") == Color.Green);
io.println(Color.fromName("nope") == null);
io.println(Color.fromName("green") == null);
`, "3\nColor.Red\nColor.Green\nColor.Blue\nColor.Green\ntrue\ntrue\ntrue\n")
}

func TestParityEnumStaticSurfaceTaggedExcluded(t *testing.T) {
	runParity(t, `import io;

enum Token { Plus, Minus, Number(int) }

list<Token> simple = Token.values();
io.println(simple.length());
for (Token tk in simple) {
    io.println(tk);
}
io.println(Token.fromName("Plus") == Token.Plus);
io.println(Token.fromName("Number") == null);
`, "2\nToken.Plus\nToken.Minus\ntrue\ntrue\n")
}

func TestParityBackedEnumString(t *testing.T) {
	runParity(t, `import io;

enum Status: string {
    Active = "active";
    Closed = "closed";
}

io.println(Status.Active.value);
io.println(Status.Closed.value);
io.println(Status.from("active") == Status.Active);
io.println(Status.tryFrom("closed") == Status.Closed);
io.println(Status.tryFrom("missing") == null);
try {
    io.println(Status.from("missing"));
} catch (Error e) {
    io.println("missing");
}
for (Status s in Status.values()) {
    io.println(s.value);
}
`, "active\nclosed\ntrue\ntrue\ntrue\nmissing\nactive\nclosed\n")
}

func TestParityBackedEnumIntAndMethods(t *testing.T) {
	runParity(t, `import io;

enum Code: int {
    Ok = 200;
    NotFound = 404;

    func isError(): bool {
        return this.value >= 400;
    }
}

io.println(Code.Ok.value);
io.println(Code.NotFound.value);
io.println(Code.from(200) == Code.Ok);
io.println(Code.tryFrom(404).isError());
io.println(Code.tryFrom(500) == null);
`, "200\n404\ntrue\ntrue\ntrue\n")
}

func TestParityEnumMatchSimpleVariants(t *testing.T) {
	runParity(t, `import io;

enum Direction { North, South, East, West }

func describe(Direction d): string {
    return match (d) {
        case Direction.North => "up";
        case Direction.South => "down";
        case Direction.East => "right";
        default => "left";
    };
}

io.println(describe(Direction.North));
io.println(describe(Direction.East));
io.println(describe(Direction.West));
`, "up\nright\nleft\n")
}

func TestParityEnumMatchTaggedVariants(t *testing.T) {
	runParity(t, `import io;

enum Result { Ok(string), Err(string) }

func handle(Result r): string {
    return match (r) {
        case Result.Ok(string msg) => "ok: " + msg;
        case Result.Err(string msg) => "err: " + msg;
    };
}

io.println(handle(Result.Ok("hello")));
io.println(handle(Result.Err("oops")));
`, "ok: hello\nerr: oops\n")
}

func TestParityOrPatterns(t *testing.T) {
	// Literal alternation.
	runParity(t, `import io;
func describe(int x): string {
    return match (x) {
        case 1 | 2 | 3 => "low";
        case 10 | 20 | 30 => "med";
        default => "other";
    };
}
io.println(describe(1));
io.println(describe(2));
io.println(describe(20));
io.println(describe(50));
`, "low\nlow\nmed\nother\n")

	// Bare-type alternation (union type form is parsed as Type).
	runParity(t, `import io;
func anyNumeric(any v): string {
    return match (v) {
        case int | float | decimal => "numeric";
        case string => "text";
        default => "other";
    };
}
io.println(anyNumeric(5));
io.println(anyNumeric(3.14));
io.println(anyNumeric("hi"));
io.println(anyNumeric(true));
`, "numeric\nnumeric\ntext\nother\n")

	// Enum-no-payload alternation.
	runParity(t, `import io;
enum Color { Red, Green, Blue }
func warm(Color c): bool {
    return match (c) {
        case Color.Red | Color.Blue => true;
        case Color.Green => false;
    };
}
io.println(warm(Color.Red));
io.println(warm(Color.Blue));
io.println(warm(Color.Green));
`, "true\ntrue\nfalse\n")

	// Guard applies to the whole or-pattern.
	runParity(t, `import io;
func withGuard(int x): string {
    return match (x) {
        case 1 | 2 | 3 if (x > 1) => "pass";
        case 1 | 2 | 3 => "fail";
        default => "other";
    };
}
io.println(withGuard(1));
io.println(withGuard(2));
io.println(withGuard(99));
`, "fail\npass\nother\n")
}

func TestParityReflectClassesEnumeratesEveryUserClass(t *testing.T) {
	runParity(t, `import io;
import reflect;

@Service
class A { func A() {} }

@Controller
class B { func B() {} }

class C { func C() {} }

let names = [];
for (cls in reflect.classes()) {
    let n = reflect.className(cls);
    if (n != null) {
        let s = n as string;
        if (s == "A" || s == "B" || s == "C") {
            names = names.push(s);
        }
    }
}
io.println(names.join(","));
`, "A,B,C\n")
}

// TestParityNullMatchesAnyParam guards a VM regression where method
// overload resolution rejected a null argument flowing into an `any`-typed
// parameter. The evaluator always accepted null for any; the VM tripped
// on the early null check in matchValueToTypeSpec before the vmTypeAny
// short-circuit. Surfaced while writing the Gebweb hello-world example
// (TestClient.send accepts `any body` and was being called with null).
func TestParityNullMatchesAnyParam(t *testing.T) {
	runParity(t, `import io;

class TestClient {
    func send(string method, any body): int {
        return 99;
    }
    func get(string path): int {
        return this.send("GET", null);
    }
}

let c = TestClient();
io.println(c.get("/"));
io.println(c.send("POST", {"k": 1}));
io.println(c.send("PUT", "raw body"));
`, "99\n99\n99\n")
}

// TestParityPCREMatchDict verifies the {text, groups, named}
// shape matches the re.match contract.
func TestParityPCREMatchDict(t *testing.T) {
	runParity(t, `import pcre;
import io;
let m = pcre.match("(?P<word>[a-z]+)(?P<num>[0-9]+)", "abc123");
io.println(m["text"] as string);
io.println(m["groups"][1] as string);
io.println(m["groups"][2] as string);
io.println(m["named"]["word"] as string);
io.println(m["named"]["num"] as string);
`, "abc123\nabc\n123\nabc\n123\n")
}

// TestParityMatchListPatterns exercises tuple-shape patterns in
// match: structural shape check, per-element type guard, _
// wildcard, length-mismatch fall-through, and the
// non-list-value case. Both backends must produce identical
// output for each branch.
func TestParityMatchListPatterns(t *testing.T) {
	runParity(t, `import io;

let pair = [3, 7];
io.println(match (pair) {
    case [int x, int y] if (x > y) => "first";
    case [int x, int y] if (x == y) => "tie";
    case [int x, int y] => "second";
    default => "n/a";
});

let mixed = ["ada", 37];
io.println(match (mixed) {
    case [int a, int b] => "two ints";
    case [string s, int n] => s + "=" + (n as string);
    default => "other";
});

let triple = [1, 2, 3];
io.println(match (triple) {
    case [int a, int b] => "two";
    case [int a, _, int c] => "wild-mid:" + ((a + c) as string);
    default => "other";
});

let notAList = "scalar";
io.println(match (notAList) {
    case [int a, int b] => "list";
    case string s => "string:" + s;
    default => "other";
});

let empty = [];
io.println(match (empty) {
    case [] => "empty";
    case [int a] => "one";
    default => "other";
});
`, "second\nada=37\nwild-mid:4\nstring:scalar\nempty\n")
}

// enumerate() pairs each element with its index, enabling indexed for-in;
// identical on both backends.
func TestParityEnumerate(t *testing.T) {
	runParity(t, `import io;
import collections;
let xs = ["a", "b", "c"];
for (i, v in xs.enumerate()) { io.println("${i}:${v}"); }
io.println(collections.enumerate([10, 20]));
`, "0:a\n1:b\n2:c\n[[0, 10], [1, 20]]\n")
}

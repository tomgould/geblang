package bytecode

import (
	"bytes"
	"strings"
	"testing"

	"geblang/internal/lexer"
	"geblang/internal/parser"
)

func runSourceOnVMExpectError(t *testing.T, src string) error {
	t.Helper()
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	chunk, err := Compile(program, []byte(src), "uncaught_render_test")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var out bytes.Buffer
	runErr := NewVM(chunk, &out).Run()
	if runErr == nil {
		t.Fatalf("expected a VM error, got none (output: %q)", out.String())
	}
	return runErr
}

func TestVMUncaughtThrowCanonicalFormat(t *testing.T) {
	src := `import io;
import errors;

func inner(int x): int {
    throw errors.new("ValueError", "boom");
}

func middle(int x): int {
    let r = inner(x);
    return r;
}

io.println(middle(5));`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at inner (line 5)" +
		"\n  at middle (line 9)" +
		"\n  at <top level> (line 13)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestVMUncaughtFaultClassedRuntimeError(t *testing.T) {
	src := `import io;

func boom(int x): int {
    return 10 / (x - x);
}

io.println(boom(3));`
	err := runSourceOnVMExpectError(t, src)
	got := err.Error()
	if !strings.HasPrefix(got, "uncaught RuntimeError: ") {
		t.Fatalf("fault must be classed RuntimeError, got: %s", got)
	}
	if strings.Contains(got, "bytecode runtime error") {
		t.Fatalf("implementation prefix leaked: %s", got)
	}
}

func TestVMReturnPositionCallKeepsCallerFrame(t *testing.T) {
	src := `import io;
import errors;

func inner(int x): int {
    throw errors.new("ValueError", "boom");
}

func middle(int x): int {
    return inner(x);
}

io.println(middle(5));`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at inner (line 5)" +
		"\n  at middle (line 9)" +
		"\n  at <top level> (line 12)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestVMMultiLineArgumentCallSiteLine(t *testing.T) {
	src := `import io;
import errors;

func makeArg(int x): int {
    return x;
}

func inner(int x): int {
    throw errors.new("ValueError", "boom");
}

func middle(int x): int {
    let r = inner(
        makeArg(x)
    );
    return r;
}

io.println(middle(5));`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at inner (line 9)" +
		"\n  at middle (line 13)" +
		"\n  at <top level> (line 19)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestVMMethodMultiLineArgumentCallSiteLine(t *testing.T) {
	src := `import io;
import errors;

class Worker {
    func compute(int x): int {
        return x + 1;
    }
    func work(int x): int {
        throw errors.new("ValueError", "boom");
    }
    func run(): int {
        let r = this.work(
            this.compute(3)
        );
        return r;
    }
}

io.println(Worker().run());`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at Worker.work (line 9)" +
		"\n  at Worker.run (line 12)" +
		"\n  at <top level> (line 19)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestVMMethodFrameQualifiedName(t *testing.T) {
	src := `import io;
import errors;

class Worker {
    func work(int x): int {
        throw errors.new("ValueError", "method boom");
    }
}

let w = Worker();
io.println(w.work(5));`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: method boom" +
		"\n  at Worker.work (line 6)" +
		"\n  at <top level> (line 11)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestVMInheritedMethodFrameUsesDeclaringClass(t *testing.T) {
	src := `import io;
import errors;

class Base {
    func boom(): int {
        throw errors.new("ValueError", "inherited boom");
    }
}

class Sub extends Base {
}

let s = Sub();
io.println(s.boom());`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: inherited boom" +
		"\n  at Base.boom (line 6)" +
		"\n  at <top level> (line 14)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestVMTopLevelThrowReportsLine(t *testing.T) {
	src := `import errors;

throw errors.new("ValueError", "top");`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: top" +
		"\n  at <top level> (line 3)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestVMSelfTailRecursionCollapses(t *testing.T) {
	src := `import io;
import errors;

func down(int n): int {
    if (n == 0) {
        throw errors.new("ValueError", "bottom");
    }
    return down(n - 1);
}

io.println(down(1000));`
	err := runSourceOnVMExpectError(t, src)
	want := "uncaught ValueError: bottom" +
		"\n  at down (line 6)" +
		"\n  at down (line 8) [x1000]" +
		"\n  at <top level> (line 11)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

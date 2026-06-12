package evaluator_test

import (
	"bytes"
	"testing"

	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

func runSourceOnEvalExpectError(t *testing.T, src string) error {
	t.Helper()
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	var out bytes.Buffer
	_, err := evaluator.New(&out).Eval(program)
	if err == nil {
		t.Fatalf("expected an evaluator error, got none (output: %q)", out.String())
	}
	return err
}

func TestEvalUncaughtThrowCanonicalFormat(t *testing.T) {
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
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at inner (line 5)" +
		"\n  at middle (line 9)" +
		"\n  at <top level> (line 13)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalReturnPositionCallKeepsCallerFrame(t *testing.T) {
	src := `import io;
import errors;

func inner(int x): int {
    throw errors.new("ValueError", "boom");
}

func middle(int x): int {
    return inner(x);
}

io.println(middle(5));`
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at inner (line 5)" +
		"\n  at middle (line 9)" +
		"\n  at <top level> (line 12)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalFaultClassedRuntimeError(t *testing.T) {
	src := `import io;

func boom(int x): int {
    return 10 / (x - x);
}

io.println(boom(3));`
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught RuntimeError: decimal division by zero" +
		"\n  at boom (line 4)" +
		"\n  at <top level> (line 7)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalMultiLineArgumentCallSiteLine(t *testing.T) {
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
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at inner (line 9)" +
		"\n  at middle (line 13)" +
		"\n  at <top level> (line 19)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalMethodMultiLineArgumentCallSiteLine(t *testing.T) {
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
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: boom" +
		"\n  at Worker.work (line 9)" +
		"\n  at Worker.run (line 12)" +
		"\n  at <top level> (line 19)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalMethodFrameQualifiedName(t *testing.T) {
	src := `import io;
import errors;

class Worker {
    func work(int x): int {
        throw errors.new("ValueError", "method boom");
    }
}

let w = Worker();
io.println(w.work(5));`
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: method boom" +
		"\n  at Worker.work (line 6)" +
		"\n  at <top level> (line 11)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalInheritedMethodFrameUsesDeclaringClass(t *testing.T) {
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
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: inherited boom" +
		"\n  at Base.boom (line 6)" +
		"\n  at <top level> (line 14)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalTopLevelThrowReportsLine(t *testing.T) {
	src := `import errors;

throw errors.new("ValueError", "top");`
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: top" +
		"\n  at <top level> (line 3)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestEvalSelfTailRecursionCollapses(t *testing.T) {
	src := `import io;
import errors;

func down(int n): int {
    if (n == 0) {
        throw errors.new("ValueError", "bottom");
    }
    return down(n - 1);
}

io.println(down(1000));`
	err := runSourceOnEvalExpectError(t, src)
	want := "uncaught ValueError: bottom" +
		"\n  at down (line 6)" +
		"\n  at down (line 8) [x1000]" +
		"\n  at <top level> (line 11)"
	if err.Error() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", err.Error(), want)
	}
}

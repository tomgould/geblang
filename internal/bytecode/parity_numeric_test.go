package bytecode_test

import (
	"bytes"
	"math"
	"strconv"
	"strings"
	"testing"

	"geblang/internal/bytecode"
	"geblang/internal/evaluator"
	"geblang/internal/lexer"
	"geblang/internal/parser"
)

// runParityNumeric keeps the eval-vs-VM check exact but compares the value pin with a relative tolerance, so transcendental results (not bit-identical across CPU architectures) do not fail off the platform where `want` was captured.
func runParityNumeric(t *testing.T, source string, want string) {
	t.Helper()

	p := parser.New(lexer.New(source))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}

	var evOut bytes.Buffer
	if _, err := evaluator.New(&evOut).Eval(program); err != nil {
		t.Fatalf("evaluator error: %v", err)
	}

	chunk, err := bytecode.Compile(program, []byte(source), "parity")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	var vmOut bytes.Buffer
	if err := bytecode.NewVM(chunk, &vmOut).Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	if evOut.String() != vmOut.String() {
		t.Errorf("output mismatch (eval vs vm must be exact):\n  evaluator: %q\n  vm:        %q", evOut.String(), vmOut.String())
	}
	if want != "" {
		assertNumericMatch(t, evOut.String(), want)
	}
}

// assertNumericMatch matches float lines within a tight relative tolerance and all other lines exactly.
func assertNumericMatch(t *testing.T, got, want string) {
	t.Helper()
	gl := strings.Split(got, "\n")
	wl := strings.Split(want, "\n")
	if len(gl) != len(wl) {
		t.Errorf("line count: got %d, want %d\n  got=%q\n  want=%q", len(gl), len(wl), got, want)
		return
	}
	for i := range gl {
		if gl[i] == wl[i] {
			continue
		}
		if gf, ge := strconv.ParseFloat(gl[i], 64); ge == nil {
			if wf, we := strconv.ParseFloat(wl[i], 64); we == nil && floatsClose(gf, wf) {
				continue
			}
		}
		t.Errorf("line %d differs: got %q, want %q", i+1, gl[i], wl[i])
	}
}

func floatsClose(a, b float64) bool {
	d := math.Abs(a - b)
	if d == 0 {
		return true
	}
	return d <= 1e-9*math.Max(math.Abs(a), math.Abs(b))
}

// TestFloatsCloseScope: the value-pin tolerance absorbs last-ULP cross-architecture noise yet catches a real divergence.
func TestFloatsCloseScope(t *testing.T) {
	tolerated := [][2]float64{
		{343.5560603410417, 343.5560603410419},
		{0.3989422804014327, 0.3989422804014329},
		{0.9750021048517795, 0.9750021048517793},
	}
	for _, p := range tolerated {
		if !floatsClose(p[0], p[1]) {
			t.Errorf("last-ULP difference %v vs %v should be tolerated", p[0], p[1])
		}
	}
	caught := [][2]float64{
		{343.556060, 343.557060},
		{0.9750021048517795, 0.9760021048517795},
		{2.5, 2.5000001},
	}
	for _, p := range caught {
		if floatsClose(p[0], p[1]) {
			t.Errorf("real difference %v vs %v must be caught", p[0], p[1])
		}
	}
}

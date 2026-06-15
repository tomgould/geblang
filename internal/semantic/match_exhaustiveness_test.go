package semantic_test

import (
	"strings"
	"testing"

	"geblang/internal/lexer"
	"geblang/internal/parser"
	"geblang/internal/semantic"
)

func matchDiags(t *testing.T, input string) []semantic.Diagnostic {
	t.Helper()
	p := parser.New(lexer.New(input))
	program := p.ParseProgram()
	if len(p.Errors()) != 0 {
		t.Fatalf("parser errors: %v", p.Errors())
	}
	return semantic.New().Analyze(program)
}

func nonExhaustiveWarning(diags []semantic.Diagnostic) *semantic.Diagnostic {
	for i := range diags {
		if diags[i].Rule == "match-nonexhaustive" {
			return &diags[i]
		}
	}
	return nil
}

const colorEnum = "enum Color { Red, Green, Blue }\n"

func TestMatchExhaustiveness(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantWarn bool
		missing  []string // substrings expected in the message when wantWarn
	}{
		{
			name: "missing one variant (expression form)",
			src: colorEnum + `func d(Color c): string {
				return match (c) {
					case Color.Red => "r";
					case Color.Green => "g";
				};
			}`,
			wantWarn: true,
			missing:  []string{"Blue"},
		},
		{
			name: "missing two variants",
			src: colorEnum + `func d(Color c): string {
				return match (c) {
					case Color.Red => "r";
				};
			}`,
			wantWarn: true,
			missing:  []string{"Green", "Blue"},
		},
		{
			name: "all variants covered",
			src: colorEnum + `func d(Color c): string {
				return match (c) {
					case Color.Red => "r";
					case Color.Green => "g";
					case Color.Blue => "b";
				};
			}`,
			wantWarn: false,
		},
		{
			name: "default makes it exhaustive",
			src: colorEnum + `func d(Color c): string {
				return match (c) {
					case Color.Red => "r";
					default => "other";
				};
			}`,
			wantWarn: false,
		},
		{
			name: "or-pattern covers remaining variants",
			src: colorEnum + `func d(Color c): string {
				return match (c) {
					case Color.Red => "r";
					case Color.Green | Color.Blue => "gb";
				};
			}`,
			wantWarn: false,
		},
		{
			name: "guarded-only variant is still missing",
			src: colorEnum + `func d(Color c): string {
				return match (c) {
					case Color.Red => "r";
					case Color.Green => "g";
					case Color.Blue if (false) => "b";
				};
			}`,
			wantWarn: true,
			missing:  []string{"Blue"},
		},
		{
			name: "statement form missing variant",
			src: colorEnum + `import io;
			func d(Color c): void {
				match (c) {
					case Color.Red: { io.println("r"); }
					case Color.Green: { io.println("g"); }
				}
			}`,
			wantWarn: true,
			missing:  []string{"Blue"},
		},
		{
			name: "non-enum subject is not flagged",
			src: `func d(int n): string {
				return match (n) {
					case 1 => "one";
					case 2 => "two";
				};
			}`,
			wantWarn: false,
		},
		{
			name: "payload variants all covered",
			src: `enum Opt { Some(int), None }
			func d(Opt o): string {
				return match (o) {
					case Opt.Some(int x) => "s";
					case Opt.None => "n";
				};
			}`,
			wantWarn: false,
		},
		{
			name: "payload variants missing one",
			src: `enum Opt { Some(int), None }
			func d(Opt o): string {
				return match (o) {
					case Opt.Some(int x) => "s";
				};
			}`,
			wantWarn: true,
			missing:  []string{"None"},
		},
		{
			name: "enum-type pattern binding is a catch-all",
			src: colorEnum + `func d(Color c): string {
				return match (c) {
					case Color.Red => "r";
					case Color other => "rest";
				};
			}`,
			wantWarn: false,
		},
		{
			name: "unknown subject type is not flagged",
			src: colorEnum + `func d(any v): string {
				return match (v) {
					case Color.Red => "r";
				};
			}`,
			wantWarn: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := matchDiags(t, tc.src)
			warn := nonExhaustiveWarning(diags)
			if tc.wantWarn {
				if warn == nil {
					t.Fatalf("expected match-nonexhaustive warning, got: %v", diags)
				}
				if warn.Severity != semantic.SeverityWarning {
					t.Errorf("severity = %d, want SeverityWarning", warn.Severity)
				}
				for _, m := range tc.missing {
					if !strings.Contains(warn.Message, m) {
						t.Errorf("warning %q missing expected variant %q", warn.Message, m)
					}
				}
			} else if warn != nil {
				t.Fatalf("unexpected match-nonexhaustive warning: %q", warn.Message)
			}
		})
	}
}

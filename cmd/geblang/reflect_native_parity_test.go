package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reflect.* name-lookups over imported native modules must resolve identically
// on the VM and the evaluator (the evaluator is canonical).
func TestReflectNativeModuleParity(t *testing.T) {
	bin := buildCMBinary(t)
	cases := []struct {
		name   string
		source string
		expect string
	}{
		{
			name:   "module",
			source: "import reflect;\nimport math;\nimport io;\nio.println(reflect.module(\"math\") != null);\n",
			expect: "true",
		},
		{
			name:   "class",
			source: "import reflect;\nimport http;\nimport io;\nio.println(reflect.class(\"http.Request\") != null);\n",
			expect: "true",
		},
		{
			name:   "function-resolves-callable",
			source: "import reflect;\nimport math;\nimport io;\nlet f = reflect.function(\"math.sqrt\");\nio.println(f != null);\nio.println(f(9.0));\n",
			expect: "true\n3",
		},
		{
			name:   "unknown-null",
			source: "import reflect;\nimport io;\nio.println(reflect.module(\"nope\") == null);\n",
			expect: "true",
		},
		{
			name:   "class-other-native-module",
			source: "import reflect;\nimport process;\nimport io;\nio.println(reflect.class(\"process.Process\") != null);\n",
			expect: "true",
		},
		{
			name:   "module-aliased",
			source: "import reflect;\nimport math as m;\nimport io;\nio.println(reflect.module(\"m\") != null);\n",
			expect: "true",
		},
		{
			name:   "canonical-not-bound-when-aliased",
			source: "import reflect;\nimport math as m;\nimport io;\nio.println(reflect.module(\"math\") == null);\n",
			expect: "true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mainPath := filepath.Join(dir, "main.gb")
			os.WriteFile(mainPath, []byte(tc.source), 0644)
			vm, eval, vmErr, evalErr := runBothBackends(t, bin, mainPath)
			assertParitySuccess(t, tc.name, vm, eval, vmErr, evalErr)
			if !strings.Contains(vm, tc.expect) {
				t.Fatalf("%s: expected output to contain %q, got: %q", tc.name, tc.expect, vm)
			}
		})
	}
}

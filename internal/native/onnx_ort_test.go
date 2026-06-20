package native

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Live ORT test; skips unless GEBLANG_ONNXRUNTIME (libonnxruntime.so) and GEBLANG_ONNX_TESTMODEL (dir with model.onnx + tokenizer.json) are set.
func ortTestPaths(t *testing.T) (string, string) {
	t.Helper()
	lib := os.Getenv("GEBLANG_ONNXRUNTIME")
	model := os.Getenv("GEBLANG_ONNX_TESTMODEL")
	if lib == "" || model == "" {
		t.Skip("set GEBLANG_ONNXRUNTIME and GEBLANG_ONNX_TESTMODEL to run live ONNX tests")
	}
	if _, err := os.Stat(lib); err != nil {
		t.Skipf("ORT lib not found: %v", err)
	}
	return lib, model
}

func TestORTEncodeSentence(t *testing.T) {
	lib, modelDir := ortTestPaths(t)
	sess, err := NewONNXSession(lib, filepath.Join(modelDir, "model.onnx"), 1)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()

	if len(sess.inputNames) == 0 || len(sess.outputNames) == 0 {
		t.Fatalf("expected inputs/outputs, got in=%v out=%v", sess.inputNames, sess.outputNames)
	}
	t.Logf("inputs=%v outputs=%v", sess.inputNames, sess.outputNames)

	tjson, err := os.ReadFile(filepath.Join(modelDir, "tokenizer.json"))
	if err != nil {
		t.Fatalf("tokenizer.json: %v", err)
	}
	tok, err := cachedTokenizer(string(tjson))
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	ids := tok.encode("hello world", 512, true)
	seq := len(ids)
	ids64 := make([]int64, seq)
	mask := make([]int64, seq)
	types := make([]int64, seq)
	for i, v := range ids {
		ids64[i] = int64(v)
		mask[i] = 1
	}
	shape := []int64{1, int64(seq)}
	inputs := map[string]ONNXInput{}
	for _, name := range sess.inputNames {
		switch name {
		case "input_ids":
			inputs[name] = ONNXInput{data: ids64, shape: shape}
		case "attention_mask":
			inputs[name] = ONNXInput{data: mask, shape: shape}
		case "token_type_ids":
			inputs[name] = ONNXInput{data: types, shape: shape}
		default:
			t.Fatalf("unexpected input %q", name)
		}
	}

	out, err := sess.Run(inputs, sess.outputNames[:1])
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res := out[sess.outputNames[0]]
	t.Logf("output %q shape=%v len=%d", sess.outputNames[0], res.shape, len(res.data))
	// all-MiniLM last_hidden_state is [1, seq, 384].
	if len(res.shape) != 3 || res.shape[0] != 1 || res.shape[1] != int64(seq) || res.shape[2] != 384 {
		t.Fatalf("unexpected output shape %v (seq=%d)", res.shape, seq)
	}
	// Mean-pool over tokens and sanity-check the embedding is finite and non-trivial.
	dim := 384
	emb := make([]float64, dim)
	for tkn := 0; tkn < seq; tkn++ {
		for d := 0; d < dim; d++ {
			emb[d] += float64(res.data[tkn*dim+d])
		}
	}
	var norm float64
	for d := 0; d < dim; d++ {
		emb[d] /= float64(seq)
		if math.IsNaN(emb[d]) || math.IsInf(emb[d], 0) {
			t.Fatalf("embedding has non-finite value at %d", d)
		}
		norm += emb[d] * emb[d]
	}
	if math.Sqrt(norm) < 1e-3 {
		t.Fatalf("embedding norm too small: %v", math.Sqrt(norm))
	}
}

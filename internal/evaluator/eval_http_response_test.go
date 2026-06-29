package evaluator

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"geblang/internal/runtime"
)

// A known-length response body gets an explicit Content-Length so the response is not chunked and the connection can be kept alive (the cached-page / static-asset hot paths).
func TestWriteHTTPResponseSetsContentLength(t *testing.T) {
	t.Run("string response", func(t *testing.T) {
		body := "hello content length body"
		rec := httptest.NewRecorder()
		(&Evaluator{}).writeHTTPResponseValue(rec, runtime.String{Value: body})
		assertContentLength(t, rec, len(body))
	})

	t.Run("dict with bytes body", func(t *testing.T) {
		body := []byte{1, 2, 3, 4, 5, 6, 7}
		dict := runtime.NewDict()
		dict.PutEntry(dictKey(runtime.String{Value: "status"}), runtime.DictEntry{Key: runtime.String{Value: "status"}, Value: runtime.SmallInt{Value: 200}})
		dict.PutEntry(dictKey(runtime.String{Value: "body"}), runtime.DictEntry{Key: runtime.String{Value: "body"}, Value: runtime.Bytes{Value: body}})
		rec := httptest.NewRecorder()
		(&Evaluator{}).writeHTTPResponseValue(rec, dict)
		assertContentLength(t, rec, len(body))
	})
}

func assertContentLength(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(want) {
		t.Fatalf("Content-Length = %q, want %d", got, want)
	}
	if got := rec.Header().Get("Transfer-Encoding"); got == "chunked" {
		t.Fatalf("response should not be chunked")
	}
}

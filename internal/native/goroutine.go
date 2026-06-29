package native

import (
	"bytes"
	"runtime"
	"strconv"
)

// sysGoroutineID returns the id of the calling goroutine. Go does not export
// this, so it is parsed from the runtime stack header ("goroutine N [...]").
// Used as a stable per-goroutine key for request-scoped state; ids are unique
// among live goroutines (reused only after one exits), so callers must clear
// their entry when the goroutine's work ends.
// GoroutineID exposes the calling goroutine's id for load-time use (module-load serialization / cycle detection), never the per-call hot path.
func GoroutineID() int64 { return sysGoroutineID() }

func sysGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	header := buf[:n]
	header = bytes.TrimPrefix(header, []byte("goroutine "))
	i := bytes.IndexByte(header, ' ')
	if i < 0 {
		return 0
	}
	id, err := strconv.ParseInt(string(header[:i]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

package transpilert

import (
	"path/filepath"
	"testing"
)

func TestIOTextRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	WriteText(p, "a\n")
	AppendText(p, "b\n")
	if got := ReadText(p); got != "a\nb\n" {
		t.Errorf("ReadText = %q", got)
	}
	if !Exists(p) {
		t.Error("Exists should be true")
	}
	Remove(p)
	if Exists(p) {
		t.Error("Exists should be false after Remove")
	}
}

func TestIOBytesRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.bin")
	WriteBytes(p, []byte{1, 2, 3})
	if got := ReadBytes(p); string(got) != "\x01\x02\x03" {
		t.Errorf("ReadBytes = %v", got)
	}
}

func TestIOReadMissingPanicsIOError(t *testing.T) {
	defer func() {
		r := recover()
		e, ok := r.(*Error)
		if !ok || e.Class != "IOError" {
			t.Errorf("expected IOError panic, got %v", r)
		}
	}()
	ReadText(filepath.Join(t.TempDir(), "missing.txt"))
}

package evaluator

import (
	"strconv"
	"strings"
	"testing"
)

func TestSyslogSeverityMapping(t *testing.T) {
	cases := map[string]int{"error": 3, "warn": 4, "info": 6, "debug": 7, "unknown": 6}
	for level, want := range cases {
		if got := syslogSeverity(level); got != want {
			t.Errorf("syslogSeverity(%q) = %d, want %d", level, got, want)
		}
	}
}

// Skips the volatile timestamp field so the test never depends on wall-clock timing.
func TestSyslogMessageFrame(t *testing.T) {
	s := &syslogSink{facility: 1, app: "testapp", host: "testhost", pid: 123, framing: framingDatagram}
	line := `{"level":"info","message":"hi"}`
	msg := s.message("info", line)

	if !strings.HasPrefix(msg, "<14>1 ") { // facility 1 (user) * 8 + severity 6 (info)
		t.Fatalf("bad PRI/version prefix: %q", msg)
	}
	fields := strings.SplitN(msg, " ", 8)
	if len(fields) != 8 {
		t.Fatalf("expected 8 space-delimited parts, got %d: %q", len(fields), msg)
	}
	if fields[2] != "testhost" || fields[3] != "testapp" || fields[4] != "123" {
		t.Errorf("host/app/pid wrong: %q %q %q", fields[2], fields[3], fields[4])
	}
	if fields[5] != "-" || fields[6] != "-" {
		t.Errorf("MSGID/SD should be NILVALUE: %q %q", fields[5], fields[6])
	}
	if fields[7] != line {
		t.Errorf("MSG = %q, want %q", fields[7], line)
	}
}

func TestSyslogFramingPerNetwork(t *testing.T) {
	line := `{"level":"error","message":"boom"}`

	octet := &syslogSink{facility: 1, app: "a", host: "h", pid: 1, framing: framingOctet}
	out := octet.framed("error", line)
	prefix, rest, ok := strings.Cut(out, " ")
	if !ok {
		t.Fatalf("octet frame missing length prefix: %q", out)
	}
	n, err := strconv.Atoi(prefix)
	if err != nil || n != len(rest) {
		t.Errorf("octet length %q does not match message length %d", prefix, len(rest))
	}

	lf := &syslogSink{facility: 1, app: "a", host: "h", pid: 1, framing: framingLF}
	if !strings.HasSuffix(lf.framed("error", line), "\n") {
		t.Error("local stream frame must end in newline")
	}

	dgram := &syslogSink{facility: 1, app: "a", host: "h", pid: 1, framing: framingDatagram}
	if strings.HasSuffix(dgram.framed("error", line), "\n") {
		t.Error("datagram frame must not append a newline")
	}
}

func TestSyslogFieldNilValue(t *testing.T) {
	if got := syslogField("   "); got != "-" {
		t.Errorf("empty field = %q, want \"-\"", got)
	}
	if got := syslogField("my app"); got != "my_app" {
		t.Errorf("spaces should become underscores: %q", got)
	}
}

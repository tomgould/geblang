package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
)

func TestRecoverableErrorClass(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", fmt.Errorf("http: %w", context.DeadlineExceeded), "TimeoutError"},
		{"net-timeout", fmt.Errorf("http: %w", &net.DNSError{IsTimeout: true}), "TimeoutError"},
		{"cert-verify", fmt.Errorf("https: %w", &tls.CertificateVerificationError{}), "TlsError"},
		{"unknown-authority", fmt.Errorf("https: %w", x509.UnknownAuthorityError{}), "TlsError"},
		{"hostname", fmt.Errorf("https: %w", x509.HostnameError{}), "TlsError"},
		{"net-op", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "IOError"},
		{"path", &os.PathError{Op: "open", Path: "/x", Err: errors.New("nope")}, "IOError"},
		{"unexpected-eof", fmt.Errorf("stream: %w", io.ErrUnexpectedEOF), "IOError"},
		{"plain", errors.New("boom"), "RuntimeError"},
	}
	for _, c := range cases {
		if got := RecoverableErrorClass(c.err); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

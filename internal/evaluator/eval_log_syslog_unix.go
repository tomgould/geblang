//go:build !windows

package evaluator

import (
	"fmt"
	"net"
)

// dialLocalSyslog connects to the local syslog daemon, datagram first then stream.
func dialLocalSyslog() (net.Conn, syslogFraming, error) {
	paths := []string{"/dev/log", "/var/run/syslog", "/var/run/log"}
	var lastErr error
	for _, p := range paths {
		if c, err := net.Dial("unixgram", p); err == nil {
			return c, framingDatagram, nil
		} else {
			lastErr = err
		}
		if c, err := net.Dial("unix", p); err == nil {
			return c, framingLF, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no local syslog socket found")
	}
	return nil, framingDatagram, lastErr
}

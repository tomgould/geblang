//go:build windows

package evaluator

import (
	"fmt"
	"net"
)

// dialLocalSyslog reports that Windows has no local syslog daemon.
func dialLocalSyslog() (net.Conn, syslogFraming, error) {
	return nil, framingDatagram, fmt.Errorf("local syslog is unavailable on Windows; use network \"udp\" or \"tcp\"")
}

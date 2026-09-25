package pxe

import (
	"context"
	"fmt"
	"net"
	"syscall"
)

// ListenHTTP pins ingress to the provisioning link as well as the literal service address.
func ListenHTTP(c Config) (net.Listener, error) {
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var controlErr error
		if err := raw.Control(func(fd uintptr) {
			controlErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, c.Interface)
		}); err != nil {
			return err
		}
		return controlErr
	}}
	l, e := lc.Listen(context.Background(), "tcp4", net.JoinHostPort(c.ServiceIP, fmt.Sprint(c.HTTPPort)))
	if e != nil {
		return nil, e
	}
	return l, nil
}

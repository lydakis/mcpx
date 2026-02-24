//go:build !linux && !darwin

package ipc

import "net"

func peerUIDMatchesCurrentUser(conn net.Conn) (bool, error) {
	return true, nil
}

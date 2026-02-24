//go:build linux

package ipc

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

func peerUIDMatchesCurrentUser(conn net.Conn) (bool, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false, fmt.Errorf("connection is not unix")
	}

	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false, err
	}

	var peerUID uint32
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			sockErr = err
			return
		}
		peerUID = cred.Uid
	}); err != nil {
		return false, err
	}
	if sockErr != nil {
		return false, sockErr
	}

	return peerUID == uint32(os.Getuid()), nil
}

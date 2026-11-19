package httphost

import (
	"net"
	"syscall"
)

func readPeerCreds(c net.Conn) (peerCreds, bool) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return peerCreds{}, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return peerCreds{}, false
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || serr != nil || cred == nil {
		return peerCreds{}, false
	}
	return peerCreds{uid: int(cred.Uid), gid: int(cred.Gid), pid: int(cred.Pid)}, true
}

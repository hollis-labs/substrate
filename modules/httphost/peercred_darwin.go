package httphost

import (
	"net"

	"golang.org/x/sys/unix"
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
	var cred *unix.Xucred
	var pid int
	var serr, perr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		pid, perr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil || serr != nil || perr != nil || cred == nil || cred.Ngroups < 1 {
		return peerCreds{}, false
	}
	// LOCAL_PEERCRED has no gid field; the first group is the peer's effective gid.
	return peerCreds{uid: int(cred.Uid), gid: int(cred.Groups[0]), pid: pid}, true
}

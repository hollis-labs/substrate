//go:build !linux && !darwin

package httphost

import "net"

// readPeerCreds is implemented on Linux (SO_PEERCRED) and darwin (LOCAL_PEERCRED).
func readPeerCreds(net.Conn) (peerCreds, bool) { return peerCreds{}, false }

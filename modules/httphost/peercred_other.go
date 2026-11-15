//go:build !linux

package httphost

import "net"

// readPeerCreds is not implemented off Linux (darwin needs x/sys/unix, not a pinned dependency yet).
func readPeerCreds(net.Conn) (peerCreds, bool) { return peerCreds{}, false }

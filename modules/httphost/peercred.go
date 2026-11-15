package httphost

import "net/http"

type peerKey struct{}

type peerCreds struct{ uid, gid, pid int }

// PeerCredentials returns the uid, gid and pid of the process on the other end of a unix
// socket connection, as the kernel reported them when it was accepted (Linux SO_PEERCRED;
// darwin LOCAL_PEERCRED and LOCAL_PEERPID, where gid is the peer's first group).
// Serve fills it for every connection on a unix listener (ServeOptions.Listener); ok is
// false on a TCP connection, on a connection a listener wrapped, outside Serve
// (httptest), and on other platforms. The pid can be reused after the peer exits; treat it
// as attribution, not as authentication. The uid is what a "same user" check compares.
func PeerCredentials(r *http.Request) (uid, gid, pid int, ok bool) {
	pc, ok := r.Context().Value(peerKey{}).(peerCreds)
	return pc.uid, pc.gid, pc.pid, ok
}

package httphost

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// ListenUnix listens on a unix socket at path whose file has exactly the permissions
// perm (0600 for the owner only, 0660 to let the group connect), for
// ServeOptions.Listener. The socket is never reachable at path with looser permissions:
// it is bound in a private (0700) directory created next to path, chmodded there, then
// linked into place, so neither the umask nor a chmod-after-bind window matters. A file
// already at path is replaced only if it is a socket nobody answers on (a stale one from
// a crash); a live socket or any other file is an error and is left alone. Closing the
// listener (Serve does on stop) removes path if it is still this socket. The directory
// holding path is the app's to create and protect.
func ListenUnix(path string, perm os.FileMode) (net.Listener, error) {
	if perm&^os.ModePerm != 0 {
		return nil, fmt.Errorf("httphost: listen unix %s: perm %v has bits beyond 0777", path, perm)
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(path), ".listen-")
	if err != nil {
		return nil, fmt.Errorf("httphost: listen unix %s: %w", path, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	tp := filepath.Join(tmp, "s")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: tp, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("httphost: %w", err)
	}
	ln.SetUnlinkOnClose(false)
	fail := func(err error) (net.Listener, error) {
		_ = ln.Close()
		return nil, fmt.Errorf("httphost: listen unix %s: %w", path, err)
	}
	if err := os.Chmod(tp, perm); err != nil {
		return fail(err)
	}
	// Link, not rename: it fails if something appeared at path meanwhile instead of
	// replacing it.
	if err := os.Link(tp, path); err != nil {
		return fail(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fail(err)
	}
	return &unixListener{UnixListener: ln, path: path, fi: fi}, nil
}

// removeStaleSocket removes path if it is a socket no process answers on.
func removeStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("httphost: listen unix %s: %w", path, err)
	}
	if fi.Mode().Type() != os.ModeSocket {
		return fmt.Errorf("httphost: listen unix %s: exists and is not a socket", path)
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = c.Close()
		return fmt.Errorf("httphost: listen unix %s: a process is listening on it", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("httphost: listen unix %s: remove stale socket: %w", path, err)
	}
	return nil
}

// unixListener is a *net.UnixListener reachable at path (it was bound under another
// name). Accept is the embedded one, so connections are *net.UnixConn, unwrapped.
type unixListener struct {
	*net.UnixListener
	path string
	fi   os.FileInfo
}

func (l *unixListener) Addr() net.Addr { return &net.UnixAddr{Name: l.path, Net: "unix"} }

func (l *unixListener) Close() error {
	err := l.UnixListener.Close()
	if fi, serr := os.Lstat(l.path); serr == nil && os.SameFile(fi, l.fi) {
		_ = os.Remove(l.path)
	}
	return err
}

// checkUnixListener refuses, in off and permissive mode, a unix socket that file
// permissions do not protect: an abstract or unnamed one (any local user in the network
// namespace can connect, as on loopback TCP) or a socket file other users may write to
// (connect to). It reads the file's mode now, so the app sets it before Serve
// (ListenUnix does).
func checkUnixListener(a *net.UnixAddr, mode libauth.Mode) error {
	if mode == libauth.ModeEnforce {
		return nil
	}
	if a.Name == "" || a.Name[0] == '@' || a.Name[0] == 0 {
		return fmt.Errorf("auth: mode %q refused on an abstract or unnamed unix socket %q (no file permissions protect it); use a socket file or enforce", mode, a.Name)
	}
	fi, err := os.Lstat(a.Name)
	if err != nil {
		return fmt.Errorf("auth: mode %q: unix socket %s: %w", mode, a.Name, err)
	}
	if fi.Mode().Type() != os.ModeSocket {
		return fmt.Errorf("auth: mode %q: %s is not a socket", mode, a.Name)
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("auth: mode %q refused on unix socket %s writable by other users (%v); remove o+w (ListenUnix) or use enforce", mode, a.Name, fi.Mode().Perm())
	}
	return nil
}

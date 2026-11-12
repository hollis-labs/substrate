package identity

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// terminal is how the commands tell a person at a keyboard from a pipe. Tests
// replace it.
var terminal = struct {
	isTerminal func(r io.Reader) (fd int, ok bool)
	readNoEcho func(fd int) ([]byte, error)
}{
	isTerminal: func(r io.Reader) (int, bool) {
		f, ok := r.(*os.File)
		if !ok || !term.IsTerminal(int(f.Fd())) {
			return 0, false
		}
		return int(f.Fd()), true
	},
	readNoEcho: func(fd int) ([]byte, error) { return term.ReadPassword(fd) },
}

const maxPasswordLine = 1024

// readPassword takes the password from a person (two no-echo prompts on stderr)
// when stdin is a terminal, and otherwise as the first line of stdin. Never from
// an argument or the environment.
func readPassword(c *cobra.Command) (string, error) {
	in := c.InOrStdin()
	if fd, ok := terminal.isTerminal(in); ok {
		errw := c.ErrOrStderr()
		_, _ = fmt.Fprint(errw, "Password: ")
		a, err := terminal.readNoEcho(fd)
		_, _ = fmt.Fprintln(errw)
		if err != nil {
			return "", errors.New("could not read the password")
		}
		_, _ = fmt.Fprint(errw, "Repeat password: ")
		b, err := terminal.readNoEcho(fd)
		_, _ = fmt.Fprintln(errw)
		if err != nil {
			return "", errors.New("could not read the password")
		}
		if string(a) != string(b) {
			return "", errors.New("the passwords do not match")
		}
		return string(a), nil
	}
	line, err := bufio.NewReaderSize(io.LimitReader(in, maxPasswordLine), maxPasswordLine).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("could not read the password from stdin")
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("no password on stdin: pipe it in, one line")
	}
	return line, nil
}

// cliCommand is the "identity" command group the module adds to the app's root
// when the cli module is selected. It runs inside the app's normal boot, so the
// config (SUBSTRATE_CONFIG merge, boot hooks) and therefore the database, the
// key file and the secrets directory are the server's.
func (s *Service) cliCommand() *cobra.Command {
	root := &cobra.Command{Use: "identity", Short: "Offline identity bootstrap (owner and API keys, no server)"}
	root.AddCommand(s.setupCmd(), s.keyCmd())
	return root
}

func (s *Service) setupCmd() *cobra.Command {
	var username, display string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Create the first owner in the database (no server; the password is read from stdin or a prompt)",
		Long: "Creates the first owner with every scope. The password is read from the terminal (prompted twice, not echoed)\n" +
			"or, when stdin is not a terminal, from its first line. It is never an argument or an environment variable.\n" +
			"Refuses when a user already exists: first-run setup closes for good.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if strings.TrimSpace(username) == "" {
				return errors.New("--username is required")
			}
			pw, err := readPassword(c)
			if err != nil {
				return err
			}
			u, err := s.SetupOwner(c.Context(), OwnerInput{Username: username, DisplayName: display, Password: pw})
			switch {
			case errors.Is(err, ErrSetupClosed):
				return errors.New("setup is already done: an owner exists; nothing changed")
			case err != nil:
				return err
			}
			_, err = fmt.Fprintf(c.OutOrStdout(), "owner %s created\n", u.Username)
			return err
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "owner's username (required)")
	cmd.Flags().StringVar(&display, "display-name", "", "owner's display name (default: the username)")
	return cmd
}

func (s *Service) keyCmd() *cobra.Command {
	group := &cobra.Command{Use: "key", Short: "API keys"}
	var name, out string
	var scopes, audiences []string
	var ttl time.Duration
	var toStdout bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create an API key without a server",
		Long: "Creates an API key for a named principal, recorded as created by " + LocalOperator + ". The key is shown once: it is\n" +
			"written to the file named by --out (new file, mode 0600, never overwritten) or, with --stdout, to stdout as\n" +
			"the only output. Any scope may be granted. Needs an owner: run setup first.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if (out == "") == !toStdout {
				return errors.New("give exactly one of --out PATH or --stdout")
			}
			in := KeyInput{Name: name, Scopes: scopes, Audiences: audiences, TTL: ttl}
			// The file is claimed before the key exists so a taken path costs nothing, and
			// removed again if the key cannot be stored.
			var f *os.File
			if out != "" {
				var err error
				f, err = os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					return fmt.Errorf("cannot create %s (it must not already exist): %w", out, err)
				}
			}
			fail := func(err error) error {
				if f != nil {
					_ = f.Close()
					_ = os.Remove(out)
				}
				return err
			}
			key, cred, err := s.CreateKey(c.Context(), in)
			if err != nil {
				return fail(err)
			}
			if f == nil {
				_, err = fmt.Fprintln(c.OutOrStdout(), key)
				return err
			}
			if _, err := f.WriteString(key + "\n"); err != nil {
				return fail(errors.New("could not write the key file"))
			}
			if err := f.Close(); err != nil {
				return fail(errors.New("could not write the key file"))
			}
			_, err = fmt.Fprintln(c.OutOrStdout(), cred.ID)
			return err
		},
	}
	create.Flags().StringVar(&name, "name", "", "principal name (required)")
	create.Flags().StringSliceVar(&scopes, "scope", nil, "scope to grant; repeat or comma-separate (required; any scope, including *)")
	create.Flags().StringSliceVar(&audiences, "audience", nil, "audience the key is limited to; repeatable")
	create.Flags().DurationVar(&ttl, "ttl", 0, "key lifetime, for example 720h (default: no expiry)")
	create.Flags().StringVar(&out, "out", "", "write the key to this new file (mode 0600)")
	create.Flags().BoolVar(&toStdout, "stdout", false, "write the key to stdout, and nothing else")
	group.AddCommand(create)
	return group
}

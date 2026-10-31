// Command connect-matrix proves app-to-app API connectivity with service
// tokens: mint, call, wrong-audience refusal, revoke, rejection within a bound.
//
//	connect-matrix mock-issuer [-listen 127.0.0.1:9100] [-iss mock] [-pub-key-out FILE]
//	connect-matrix run -config FILE [-only NAME,...] [-json FILE] [-matrix]
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	cm "github.com/hollis-labs/tools/connect-matrix"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "mock-issuer":
		err = mockIssuer(ctx, os.Args[2:])
	case "run":
		var ok bool
		ok, err = run(ctx, os.Args[2:])
		if err == nil && !ok {
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect-matrix:", err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  connect-matrix mock-issuer [-listen ADDR] [-iss NAME] [-pub-key-out FILE]
  connect-matrix run -config FILE [-only NAME,...] [-json FILE] [-matrix]`)
}

func mockIssuer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mock-issuer", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:9100", "listen address (loopback or tailnet only)")
	iss := fs.String("iss", "mock", `"iss" claim; configure targets with the same value`)
	pubOut := fs.String("pub-key-out", "", "write the PEM public key here (a target's bootstrap key file)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || (!ip.IsLoopback() && !isTailnet(ip))) {
		return fmt.Errorf("refusing to serve a token minting endpoint on %q: use loopback or a tailnet address", *listen)
	}
	m, err := cm.NewMockIssuer(*iss)
	if err != nil {
		return err
	}
	if *pubOut != "" {
		b, err := m.PublicKeyPEM()
		if err != nil {
			return err
		}
		if err := os.WriteFile(*pubOut, b, 0o600); err != nil {
			return err
		}
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	fmt.Printf("mock issuer on http://%s  iss=%s\n  jwks: /.well-known/jwks.json  deny-list: /auth/denylist\n", ln.Addr(), *iss)
	srv := &http.Server{Handler: m.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// isTailnet reports whether ip is in the shared CGNAT range (a /10 starting at
// 100.64) or the tailnet ULA /48, the address ranges tailnets hand out.
func isTailnet(ip net.IP) bool {
	cgnat := net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	ula := net.IPNet{IP: net.ParseIP("fd7a:115c:a1e0::"), Mask: net.CIDRMask(48, 128)}
	return cgnat.Contains(ip) || ula.Contains(ip)
}

func run(ctx context.Context, args []string) (bool, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config file (JSON)")
	only := fs.String("only", "", "comma-separated pair names to run")
	jsonOut := fs.String("json", "", "also write results as JSON to this file")
	matrix := fs.Bool("matrix", false, "print the caller x target matrix before the table")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *cfgPath == "" {
		return false, fmt.Errorf("-config is required")
	}
	cfg, err := cm.LoadConfig(*cfgPath)
	if err != nil {
		return false, err
	}
	iss, err := cm.NewIssuer(cfg.Issuer, nil)
	if err != nil {
		return false, err
	}
	sel := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			sel[n] = true
		}
	}
	r := &cm.Runner{Issuer: iss, Cfg: cfg}
	results := r.Run(ctx, sel)
	if *matrix {
		cm.WriteMatrix(os.Stdout, results)
	}
	cm.WriteTable(os.Stdout, results)
	if *jsonOut != "" {
		f, err := os.OpenFile(*jsonOut, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return false, err
		}
		defer func() { _ = f.Close() }()
		if err := cm.WriteJSON(f, results); err != nil {
			return false, err
		}
	}
	return cm.AllPassed(results), nil
}
